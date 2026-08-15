package git

import (
	"context"
	"strings"

	"github.com/kunmi02/git-declutter/internal/safety"
)

func (r *Repo) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, code, err := r.runner.GitAllowFail(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

func (r *Repo) HeadRelation(ctx context.Context, localSHA, otherSHA string) (safety.HeadRelation, error) {
	if localSHA == "" || otherSHA == "" {
		return safety.RelUnknown, nil
	}
	if localSHA == otherSHA {
		return safety.RelEqual, nil
	}
	localIsAncestor, err := r.IsAncestor(ctx, localSHA, otherSHA)
	if err != nil {
		return safety.RelUnknown, err
	}
	otherIsAncestor, err := r.IsAncestor(ctx, otherSHA, localSHA)
	if err != nil {
		return safety.RelUnknown, err
	}
	switch {
	case localIsAncestor && !otherIsAncestor:
		return safety.RelBehind, nil
	case otherIsAncestor && !localIsAncestor:
		return safety.RelAhead, nil
	case localIsAncestor && otherIsAncestor:
		return safety.RelEqual, nil
	default:
		return safety.RelDiverged, nil
	}
}

func (r *Repo) LocalOnlyCommits(ctx context.Context, branch string) ([]safety.Commit, error) {
	out, err := r.runner.Git(ctx, "rev-list", "--pretty=format:%s", "refs/heads/"+branch, "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	return parseRevList(out), nil
}

// LastKnownRemoteSHA is the current remote-tracking SHA, or the latest
// reflog entry after that ref was pruned (so we can still see the last push).
func (r *Repo) LastKnownRemoteSHA(ctx context.Context, remote, branch string) (string, error) {
	if sha, err := r.RemoteTrackingSHA(ctx, remote, branch); err != nil {
		return "", err
	} else if sha != "" {
		return sha, nil
	}
	if remote == "" {
		remote = "origin"
	}
	ref := "refs/remotes/" + remote + "/" + branch
	out, code, err := r.runner.GitAllowFail(ctx, "reflog", "-n", "1", "--format=%H", ref)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

func (r *Repo) CommitsNotIn(ctx context.Context, branch, other string) ([]safety.Commit, error) {
	out, err := r.runner.Git(ctx, "rev-list", "--pretty=format:%s", other+".."+branch)
	if err != nil {
		return nil, err
	}
	return parseRevList(out), nil
}

func parseRevList(out string) []safety.Commit {
	var commits []safety.Commit
	var current safety.Commit
	for _, line := range splitLines(out) {
		if strings.HasPrefix(line, "commit ") {
			if current.SHA != "" {
				commits = append(commits, current)
			}
			current = safety.Commit{SHA: strings.TrimPrefix(line, "commit ")}
			continue
		}
		if current.SHA != "" && current.Subject == "" {
			current.Subject = line
		}
	}
	if current.SHA != "" {
		commits = append(commits, current)
	}
	return commits
}
