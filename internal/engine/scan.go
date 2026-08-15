package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kunmi02/git-declutter/internal/config"
	"github.com/kunmi02/git-declutter/internal/git"
	"github.com/kunmi02/git-declutter/internal/providers"
	gh "github.com/kunmi02/git-declutter/internal/providers/github"
	gl "github.com/kunmi02/git-declutter/internal/providers/gitlab"
	"github.com/kunmi02/git-declutter/internal/recovery"
	"github.com/kunmi02/git-declutter/internal/safety"
)

type ScanOptions struct {
	Offline bool
	Refresh bool
	Branch  string
}

type ScanResult struct {
	Repository   RepoInfo                `json:"repository"`
	Summary      Summary                 `json:"summary"`
	Branches     []safety.BranchAnalysis `json:"branches"`
	AuthMessage  providers.AuthMessage   `json:"-"`
	ProviderNote string                  `json:"-"`
	Refreshed    bool                    `json:"-"`
}

type RepoInfo struct {
	Path          string `json:"path"`
	Name          string `json:"name,omitempty"`
	DefaultBranch string `json:"defaultBranch"`
	Provider      string `json:"provider,omitempty"`
}

type Summary struct {
	Safe      int `json:"safe"`
	Review    int `json:"review"`
	Keep      int `json:"keep"`
	Protected int `json:"protected"`
}

func Scan(ctx context.Context, repo *git.Repo, cfg config.Config, opts ScanOptions) (*ScanResult, error) {
	store := recovery.Store{Repo: repo}
	_, _ = store.CleanupExpired(ctx)

	branches, err := repo.Branches(ctx)
	if err != nil {
		return nil, err
	}

	remote, _ := repo.PrimaryRemote(ctx)
	remoteHeads := loadRemoteHeads(ctx, repo)
	for name, sha := range snapshotTrackingHeads(ctx, repo, branches, remote.Name) {
		remoteHeads[name] = sha
	}
	_ = saveRemoteHeads(ctx, repo, remoteHeads)

	if opts.Refresh {
		if err := repo.FetchPrune(ctx); err != nil {
			return nil, fmt.Errorf("refresh remotes: %w", err)
		}
		branches, err = repo.Branches(ctx)
		if err != nil {
			return nil, err
		}
	}
	if opts.Branch != "" {
		filtered := branches[:0]
		for _, b := range branches {
			if b.Name == opts.Branch {
				filtered = append(filtered, b)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("branch %q not found", opts.Branch)
		}
		branches = filtered
	}

	trees, err := repo.Worktrees(ctx)
	if err != nil {
		return nil, err
	}
	inWorktree := git.WorktreeBranches(trees)
	current, _ := repo.CurrentBranch(ctx)

	parsed, _ := git.ParseRemoteURL(remote.URL)

	defaultBranch, defaultKnown := detectDefault(ctx, repo, remote.Name, nil)

	var (
		provider     providers.Provider
		auth         providers.AuthMessage
		providerNote string
		repoMeta     *providers.Repository
		prsByBranch  map[string][]safety.PullRequest
	)

	if !opts.Offline && parsed.Provider != "" {
		provider, auth = openProvider(parsed, cfg)
		if provider != nil {
			cached, cacheErr := loadPRCache(ctx, repo, parsed)
			useCache := cacheErr == nil && cached != nil && time.Since(cached.FetchedAt) < config.CacheTTL
			if useCache {
				repoMeta = cached.Repository
				prsByBranch = indexPRs(cached.PullRequests)
			} else {
				meta, err := provider.Repository(ctx)
				if err != nil {
					providerNote = providerErrorNote(err)
					provider = nil
				} else {
					repoMeta = meta
					prs, err := provider.PullRequests(ctx)
					if err != nil {
						providerNote = providerErrorNote(err)
						provider = nil
					} else {
						prsByBranch = indexPRs(prs)
						_ = savePRCache(ctx, repo, parsed, &providers.CacheEntry{
							FetchedAt:    time.Now().UTC(),
							Repository:   meta,
							PullRequests: prs,
						})
					}
				}
			}
		} else if auth.Warning != "" {
			providerNote = auth.Warning
		}
	} else if opts.Offline {
		providerNote = "Offline mode: local Git analysis only."
	}

	if repoMeta != nil && repoMeta.DefaultBranch != "" {
		defaultBranch = repoMeta.DefaultBranch
		defaultKnown = true
	}
	if !defaultKnown {
		defaultBranch, defaultKnown = detectDefault(ctx, repo, remote.Name, repoMeta)
	}

	analyzer := safety.NewAnalyzer(cfg.Cleanup.RequireRemoteDeleted)
	now := time.Now().UTC()
	analyses := make([]safety.BranchAnalysis, 0, len(branches))

	for _, b := range branches {
		bctx, err := buildContext(ctx, repo, cfg, b, current, inWorktree, defaultBranch, defaultKnown, remote.Name, remoteHeads, prsByBranch, provider, opts.Offline, now)
		if err != nil {
			return nil, err
		}
		if provider == nil && !opts.Offline && parsed.Provider != "" {
			bctx.ProviderError = providerNote
		}
		analyses = append(analyses, analyzer.Analyze(bctx))
	}

	result := &ScanResult{
		Repository: RepoInfo{
			Path:          repo.Path(),
			Name:          filepath.Base(repo.Path()),
			DefaultBranch: defaultBranch,
			Provider:      parsed.Provider,
		},
		Branches:     analyses,
		AuthMessage:  auth,
		ProviderNote: providerNote,
		Refreshed:    opts.Refresh,
	}
	if repoMeta != nil && repoMeta.Name != "" {
		result.Repository.Name = repoMeta.Name
	}
	for _, a := range analyses {
		switch a.Status {
		case safety.StatusSafe:
			result.Summary.Safe++
		case safety.StatusReview:
			result.Summary.Review++
		case safety.StatusKeep:
			result.Summary.Keep++
		case safety.StatusProtected:
			result.Summary.Protected++
		}
	}
	return result, nil
}

func buildContext(
	ctx context.Context,
	repo *git.Repo,
	cfg config.Config,
	b git.Branch,
	current string,
	inWorktree map[string]bool,
	defaultBranch string,
	defaultKnown bool,
	remoteName string,
	remoteHeads map[string]string,
	prsByBranch map[string][]safety.PullRequest,
	_ providers.Provider,
	offline bool,
	now time.Time,
) (safety.BranchContext, error) {
	protected, pattern := config.MatchesProtected(cfg.Protected, b.Name)
	isDefault := defaultKnown && b.Name == defaultBranch
	merged := false
	if defaultKnown && defaultBranch != "" && b.Name != defaultBranch {
		ok, err := repo.IsAncestor(ctx, b.SHA, defaultBranch)
		if err != nil {
			return safety.BranchContext{}, err
		}
		merged = ok
	}

	localOnly, err := repo.LocalOnlyCommits(ctx, b.Name)
	if err != nil {
		localOnly = nil
	}

	remote := b.RemoteName
	if remote == "" {
		remote = remoteName
	}
	lastRemoteSHA := ""
	if remoteHeads != nil {
		lastRemoteSHA = remoteHeads[b.Name]
	}
	if lastRemoteSHA == "" {
		lastRemoteSHA, _ = repo.LastKnownRemoteSHA(ctx, remote, b.Name)
	}

	remoteState := safety.RemoteUnknown
	if b.Upstream != "" {
		sha, err := repo.RemoteTrackingSHA(ctx, b.RemoteName, strings.TrimPrefix(b.Upstream, b.RemoteName+"/"))
		if err == nil && sha != "" {
			remoteState = safety.RemoteExists
		} else {
			remoteState = safety.RemoteDeleted
		}
	} else if remoteName != "" {
		sha, err := repo.RemoteTrackingSHA(ctx, remoteName, b.Name)
		if err == nil && sha != "" {
			remoteState = safety.RemoteExists
		} else if lastRemoteSHA != "" {
			remoteState = safety.RemoteDeleted
		} else {
			remoteState = safety.RemoteUnknown
		}
	}

	prs := prsByBranch[b.Name]
	mergedPR := providers.MostRecentMerged(prs)
	rel := safety.RelNone
	if mergedPR != nil {
		rel, err = repo.HeadRelation(ctx, b.SHA, mergedPR.HeadSHA)
		if err != nil {
			rel = safety.RelUnknown
		}
	}

	return safety.BranchContext{
		Name:               b.Name,
		LocalSHA:           b.SHA,
		Upstream:           b.Upstream,
		RemoteName:         b.RemoteName,
		RemoteState:        remoteState,
		IsCurrent:          current != "" && b.Name == current,
		InWorktree:         inWorktree[b.Name] && b.Name != current,
		IsProtected:        protected && !isDefault,
		ProtectedPattern:   pattern,
		IsDefault:          isDefault,
		IsMergedToTrunk:    merged,
		DefaultKnown:       defaultKnown,
		DefaultBranch:      defaultBranch,
		LocalOnlyCommits:   localOnly,
		LastKnownRemoteSHA: lastRemoteSHA,
		PullRequests:       prs,
		MergedPR:           mergedPR,
		PRHeadRelation:     rel,
		Offline:            offline,
		AnalyzedAt:         now,
	}, nil
}

func detectDefault(ctx context.Context, repo *git.Repo, remoteName string, meta *providers.Repository) (string, bool) {
	if remoteName == "" {
		remoteName = "origin"
	}
	if name, err := repo.DefaultBranchFromRemoteHEAD(ctx, remoteName); err == nil && name != "" {
		if ok, _ := repo.BranchExists(ctx, name); ok {
			return name, true
		}
		return name, true
	}
	if meta != nil && meta.DefaultBranch != "" {
		return meta.DefaultBranch, true
	}
	for _, candidate := range []string{"main", "master"} {
		ok, err := repo.BranchExists(ctx, candidate)
		if err == nil && ok {
			return candidate, true
		}
	}
	return "", false
}

func openProvider(remote git.RemoteProvider, cfg config.Config) (providers.Provider, providers.AuthMessage) {
	switch remote.Provider {
	case "github":
		if !cfg.Providers.GitHub.Enabled {
			return nil, providers.AuthMessage{}
		}
		c := gh.New(remote)
		return c, c.AuthMessage()
	case "gitlab":
		if !cfg.Providers.GitLab.Enabled {
			return nil, providers.AuthMessage{}
		}
		c := gl.New(remote)
		return c, c.AuthMessage()
	default:
		return nil, providers.AuthMessage{}
	}
}

func indexPRs(prs []safety.PullRequest) map[string][]safety.PullRequest {
	out := make(map[string][]safety.PullRequest)
	for _, pr := range prs {
		if pr.HeadBranch == "" {
			continue
		}
		out[pr.HeadBranch] = append(out[pr.HeadBranch], pr)
	}
	return out
}

func providerErrorNote(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "authentication required") {
		return "GitHub/GitLab authentication unavailable.\nRunning with local Git analysis only.\n\nSome branches may be marked REVIEW."
	}
	return fmt.Sprintf("Provider unavailable.\n\nContinuing with local Git analysis.\nProvider-dependent branches will be marked REVIEW.")
}

func loadPRCache(ctx context.Context, repo *git.Repo, remote git.RemoteProvider) (*providers.CacheEntry, error) {
	dir, err := cacheDir(ctx, repo)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, cacheFileName(remote))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry providers.CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func savePRCache(ctx context.Context, repo *git.Repo, remote git.RemoteProvider, entry *providers.CacheEntry) error {
	dir, err := cacheDir(ctx, repo)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, cacheFileName(remote)), data, 0o644)
}

func cacheDir(ctx context.Context, repo *git.Repo) (string, error) {
	common, err := repo.CommonDir(ctx)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repo.Path(), common)
	}
	return filepath.Join(common, "git-declutter", "cache"), nil
}

func cacheFileName(remote git.RemoteProvider) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(remote.Host + "_" + remote.Owner + "_" + remote.Repo)
	return safe + "-prs.json"
}

type remoteHeadCache struct {
	Heads map[string]string `json:"heads"`
}

func snapshotTrackingHeads(ctx context.Context, repo *git.Repo, branches []git.Branch, remoteName string) map[string]string {
	out := make(map[string]string, len(branches))
	for _, b := range branches {
		remote := b.RemoteName
		if remote == "" {
			remote = remoteName
		}
		name := b.Name
		if b.Upstream != "" && remote != "" {
			name = strings.TrimPrefix(b.Upstream, remote+"/")
		}
		sha, err := repo.RemoteTrackingSHA(ctx, remote, name)
		if err != nil || sha == "" {
			continue
		}
		out[b.Name] = sha
	}
	return out
}

func loadRemoteHeads(ctx context.Context, repo *git.Repo) map[string]string {
	dir, err := cacheDir(ctx, repo)
	if err != nil {
		return map[string]string{}
	}
	data, err := os.ReadFile(filepath.Join(dir, "remote-heads.json"))
	if err != nil {
		return map[string]string{}
	}
	var c remoteHeadCache
	if err := json.Unmarshal(data, &c); err != nil || c.Heads == nil {
		return map[string]string{}
	}
	return c.Heads
}

func saveRemoteHeads(ctx context.Context, repo *git.Repo, heads map[string]string) error {
	dir, err := cacheDir(ctx, repo)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(remoteHeadCache{Heads: heads})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "remote-heads.json"), data, 0o644)
}

func FindAnalysis(result *ScanResult, name string) (safety.BranchAnalysis, bool) {
	for _, a := range result.Branches {
		if a.Branch == name {
			return a, true
		}
	}
	return safety.BranchAnalysis{}, false
}
