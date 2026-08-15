package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kunmi02/git-declutter/internal/config"
	"github.com/kunmi02/git-declutter/internal/engine"
	"github.com/kunmi02/git-declutter/internal/git"
	"github.com/kunmi02/git-declutter/internal/recovery"
	"github.com/kunmi02/git-declutter/internal/safety"
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("git"); err != nil {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type harness struct {
	t      *testing.T
	root   string
	remote string
	local  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	h := &harness{
		t:      t,
		root:   root,
		remote: filepath.Join(root, "remote.git"),
		local:  filepath.Join(root, "local"),
	}
	h.git(root, "init", "--bare", h.remote)
	if err := os.MkdirAll(h.local, 0o755); err != nil {
		t.Fatal(err)
	}
	h.git(h.local, "init")
	h.git(h.local, "config", "user.email", "test@example.com")
	h.git(h.local, "config", "user.name", "GitDeclutter Test")
	h.git(h.local, "checkout", "-B", "main")
	h.git(h.local, "remote", "add", "origin", h.remote)
	h.write("README.md", "hello\n")
	h.git(h.local, "add", "README.md")
	h.git(h.local, "commit", "-m", "init")
	h.git(h.local, "push", "-u", "origin", "main")
	h.git(h.local, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return h
}

func (h *harness) git(dir string, args ...string) {
	h.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (h *harness) write(rel, contents string) {
	h.t.Helper()
	path := filepath.Join(h.local, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) repo(t *testing.T) *git.Repo {
	t.Helper()
	r, err := git.Open(context.Background(), h.local)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (h *harness) scanOffline(t *testing.T) *engine.ScanResult {
	t.Helper()
	cfg := config.Defaults()
	res, err := engine.Scan(context.Background(), h.repo(t), cfg, engine.ScanOptions{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func statusOf(res *engine.ScanResult, name string) safety.BranchStatus {
	a, ok := engine.FindAnalysis(res, name)
	if !ok {
		return ""
	}
	return a.Status
}

func TestMergedBranchIsSafe(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "feature/oauth")
	h.write("oauth.go", "package oauth\n")
	h.git(h.local, "add", "oauth.go")
	h.git(h.local, "commit", "-m", "oauth")
	h.git(h.local, "push", "-u", "origin", "feature/oauth")
	h.git(h.local, "checkout", "main")
	h.git(h.local, "merge", "--no-ff", "feature/oauth", "-m", "merge oauth")
	h.git(h.local, "push", "origin", "main")
	h.git(h.local, "push", "origin", "--delete", "feature/oauth")
	h.git(h.local, "fetch", "--prune")

	res := h.scanOffline(t)
	if st := statusOf(res, "feature/oauth"); st != safety.StatusSafe {
		a, _ := engine.FindAnalysis(res, "feature/oauth")
		t.Fatalf("feature/oauth status=%s reasons=%v", st, a.Reasons)
	}
	if st := statusOf(res, "main"); st != safety.StatusProtected {
		t.Fatalf("main status=%s", st)
	}
}

func TestLocalCommitsAfterMergeAreKeep(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "feature/payments")
	h.write("pay.go", "package pay\n")
	h.git(h.local, "add", "pay.go")
	h.git(h.local, "commit", "-m", "pay")
	h.git(h.local, "push", "-u", "origin", "feature/payments")
	h.git(h.local, "checkout", "main")
	h.git(h.local, "merge", "--no-ff", "feature/payments", "-m", "merge pay")
	h.git(h.local, "push", "origin", "main")
	h.git(h.local, "push", "origin", "--delete", "feature/payments")
	h.git(h.local, "fetch", "--prune")

	h.git(h.local, "checkout", "feature/payments")
	h.write("pay.go", "package pay\n\nfunc Extra() {}\n")
	h.git(h.local, "add", "pay.go")
	h.git(h.local, "commit", "-m", "local only extra")
	h.git(h.local, "checkout", "main")

	res := h.scanOffline(t)
	if st := statusOf(res, "feature/payments"); st != safety.StatusKeep {
		a, _ := engine.FindAnalysis(res, "feature/payments")
		t.Fatalf("critical regression: status=%s reasons=%v summary=%s", st, a.Reasons, a.Summary)
	}
}

func TestWorktreeIsProtected(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "feature/foo")
	h.write("foo.go", "package foo\n")
	h.git(h.local, "add", "foo.go")
	h.git(h.local, "commit", "-m", "foo")
	h.git(h.local, "checkout", "main")
	wt := filepath.Join(h.root, "worktree")
	h.git(h.local, "worktree", "add", wt, "feature/foo")

	res := h.scanOffline(t)
	if st := statusOf(res, "feature/foo"); st != safety.StatusProtected {
		a, _ := engine.FindAnalysis(res, "feature/foo")
		t.Fatalf("worktree branch status=%s reasons=%v", st, a.Reasons)
	}
}

func TestRecoveryRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "feature/login")
	h.write("login.go", "package login\n")
	h.git(h.local, "add", "login.go")
	h.git(h.local, "commit", "-m", "login")
	shaOut, err := exec.Command("git", "-C", h.local, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := string(shaOut[:len(shaOut)-1])
	h.git(h.local, "checkout", "main")

	repo := h.repo(t)
	store := recovery.Store{Repo: repo}
	cfg := config.Defaults()
	ev, err := store.Create(context.Background(), "feature/login", sha, "merged_pr", 481, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteBranch(context.Background(), "feature/login"); err != nil {
		t.Fatal(err)
	}
	exists, err := repo.BranchExists(context.Background(), "feature/login")
	if err != nil || exists {
		t.Fatalf("branch should be gone: %v %v", exists, err)
	}
	refs, err := repo.ListRefs(context.Background(), "refs/git-declutter/recovery")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("expected recovery ref")
	}

	got, err := store.Restore(context.Background(), "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != ev.SHA {
		t.Fatalf("restored sha %s want %s", got.SHA, ev.SHA)
	}
	exists, err = repo.BranchExists(context.Background(), "feature/login")
	if err != nil || !exists {
		t.Fatal("branch should be restored")
	}
	head, err := repo.RevParse(context.Background(), "refs/heads/feature/login")
	if err != nil {
		t.Fatal(err)
	}
	if head != sha {
		t.Fatalf("restored HEAD %s want %s", head, sha)
	}
}

func TestPermanentDeleteHasNoRecoveryRef(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "gone")
	h.write("gone.go", "package gone\n")
	h.git(h.local, "add", "gone.go")
	h.git(h.local, "commit", "-m", "gone")
	sha, _ := exec.Command("git", "-C", h.local, "rev-parse", "HEAD").Output()
	h.git(h.local, "checkout", "main")

	repo := h.repo(t)
	store := recovery.Store{Repo: repo}
	_, err := store.CreatePermanent(context.Background(), "gone", string(sha[:len(sha)-1]), "merged_into_trunk", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteBranch(context.Background(), "gone"); err != nil {
		t.Fatal(err)
	}
	refs, err := repo.ListRefs(context.Background(), "refs/git-declutter/recovery")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("permanent delete must not create recovery refs: %v", refs)
	}
}

func TestExpiredRecoveryRemoved(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "old")
	h.write("old.go", "package old\n")
	h.git(h.local, "add", "old.go")
	h.git(h.local, "commit", "-m", "old")
	shaOut, _ := exec.Command("git", "-C", h.local, "rev-parse", "HEAD").Output()
	sha := string(shaOut[:len(shaOut)-1])
	h.git(h.local, "checkout", "main")

	repo := h.repo(t)
	store := recovery.Store{Repo: repo}
	cfg := config.Defaults()
	ev, err := store.Create(context.Background(), "old", sha, "merged_into_trunk", 0, cfg)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	ev.ExpiresAt = &past
	events, _ := store.Load(context.Background())
	for i := range events {
		if events[i].ID == ev.ID {
			events[i].ExpiresAt = &past
		}
	}
	if err := store.SaveAll(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	n, err := store.CleanupExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired removed=%d", n)
	}
}

func TestCleanSkipsWhenSHAChanges(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "feature/oauth")
	h.write("oauth.go", "package oauth\n")
	h.git(h.local, "add", "oauth.go")
	h.git(h.local, "commit", "-m", "oauth")
	h.git(h.local, "push", "-u", "origin", "feature/oauth")
	h.git(h.local, "checkout", "main")
	h.git(h.local, "merge", "--no-ff", "feature/oauth", "-m", "merge oauth")
	h.git(h.local, "push", "origin", "main")
	h.git(h.local, "push", "origin", "--delete", "feature/oauth")
	h.git(h.local, "fetch", "--prune")

	res := h.scanOffline(t)
	a, ok := engine.FindAnalysis(res, "feature/oauth")
	if !ok || a.Status != safety.StatusSafe {
		t.Fatalf("expected SAFE, got %+v", a)
	}
	a.SHA = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	cfg := config.Defaults()
	out, err := engine.Clean(context.Background(), h.repo(t), cfg, []safety.BranchAnalysis{a}, engine.CleanOptions{SafeOnly: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Skipped) != 1 {
		t.Fatalf("expected skip on SHA change, got %+v", out)
	}
}

func TestCleanRefusesNonSafe(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "experiment")
	h.write("exp.go", "package exp\n")
	h.git(h.local, "add", "exp.go")
	h.git(h.local, "commit", "-m", "exp")
	h.git(h.local, "checkout", "main")

	keep := safety.BranchAnalysis{
		Branch:  "experiment",
		SHA:     "ignored",
		Status:  safety.StatusKeep,
		Reasons: []safety.Reason{safety.ReasonLocalOnlyCommits},
	}
	cfg := config.Defaults()
	out, err := engine.Clean(context.Background(), h.repo(t), cfg, []safety.BranchAnalysis{keep}, engine.CleanOptions{SafeOnly: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Deleted) != 0 {
		t.Fatal("KEEP must never be deleted")
	}
}

func TestNotAGitRepository(t *testing.T) {
	dir := t.TempDir()
	_, err := git.Open(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAbandonedAfterRemoteDeleteIsSafe(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "feat/try-out")
	h.write("try.go", "package try\n")
	h.git(h.local, "add", "try.go")
	h.git(h.local, "commit", "-m", "try")
	h.git(h.local, "push", "-u", "origin", "feat/try-out")
	h.git(h.local, "checkout", "main")
	// Delete on the remote only, like the GitHub UI. Local origin/* still exists until prune.
	h.git(h.remote, "branch", "-D", "feat/try-out")

	cfg := config.Defaults()
	res, err := engine.Scan(context.Background(), h.repo(t), cfg, engine.ScanOptions{Offline: true, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := engine.FindAnalysis(res, "feat/try-out")
	if !ok || a.Status != safety.StatusSafe {
		t.Fatalf("expected SAFE abandoned branch, got %+v", a)
	}
	if !a.HasReason(safety.ReasonAbandonedRemoteDeleted) {
		t.Fatalf("expected abandoned_remote_deleted, got %v", a.Reasons)
	}
}

func TestMergedIntoNonDefaultRemoteIsSafe(t *testing.T) {
	h := newHarness(t)
	h.git(h.local, "checkout", "-b", "develop")
	h.git(h.local, "push", "-u", "origin", "develop")
	h.git(h.local, "checkout", "-b", "feat/into-develop")
	h.write("child.go", "package child\n")
	h.git(h.local, "add", "child.go")
	h.git(h.local, "commit", "-m", "child")
	h.git(h.local, "push", "-u", "origin", "feat/into-develop")
	h.git(h.local, "checkout", "develop")
	h.git(h.local, "merge", "--no-ff", "feat/into-develop", "-m", "merge child")
	h.git(h.local, "push", "origin", "develop")
	h.git(h.local, "push", "origin", "--delete", "feat/into-develop")
	h.git(h.local, "checkout", "main")
	h.git(h.local, "fetch", "--prune")

	res := h.scanOffline(t)
	a, ok := engine.FindAnalysis(res, "feat/into-develop")
	if !ok || a.Status != safety.StatusSafe {
		t.Fatalf("expected SAFE when merged into develop, got %+v", a)
	}
	if a.HasReason(safety.ReasonMergedIntoTrunk) {
		t.Fatal("should not be classified as merged into main")
	}
}
