package safety

import (
	"fmt"
	"strings"
)

const SafeConfidenceThreshold = 90

type Analyzer struct {
	RequireRemoteDeleted bool
}

func NewAnalyzer(requireRemoteDeleted bool) Analyzer {
	return Analyzer{RequireRemoteDeleted: requireRemoteDeleted}
}

func (a Analyzer) Analyze(ctx BranchContext) BranchAnalysis {
	out := BranchAnalysis{
		Branch:     ctx.Name,
		SHA:        ctx.LocalSHA,
		AnalyzedAt: ctx.AnalyzedAt,
		Evidence: Evidence{
			LocalSHA:           ctx.LocalSHA,
			DefaultBranch:      ctx.DefaultBranch,
			RemoteState:        ctx.RemoteState,
			MergedIntoTrunk:    ctx.IsMergedToTrunk,
			LastKnownRemoteSHA: ctx.LastKnownRemoteSHA,
			Worktree:           ctx.InWorktree,
			Protected:          ctx.IsProtected || ctx.IsCurrent || ctx.IsDefault || ctx.InWorktree,
			PRHeadRelation:     ctx.PRHeadRelation,
		},
	}
	if len(ctx.LocalOnlyCommits) > 0 {
		out.Evidence.LocalOnlyCommitSHAs = make([]string, 0, len(ctx.LocalOnlyCommits))
		for _, c := range ctx.LocalOnlyCommits {
			out.Evidence.LocalOnlyCommitSHAs = append(out.Evidence.LocalOnlyCommitSHAs, c.SHA)
		}
	}
	if ctx.MergedPR != nil {
		pr := *ctx.MergedPR
		out.Evidence.PullRequest = &pr
		out.PullRequest = &pr
	} else if open := firstOpenPR(ctx.PullRequests); open != nil {
		pr := *open
		out.Evidence.PullRequest = &pr
		out.PullRequest = &pr
	} else if len(ctx.PullRequests) > 0 {
		pr := ctx.PullRequests[0]
		out.Evidence.PullRequest = &pr
		out.PullRequest = &pr
	}

	result := a.evaluate(ctx)
	out.Status = result.status
	out.Confidence = result.confidence
	out.Reasons = result.reasons
	out.ReasonDetails = result.details
	if out.Status == StatusSafe && out.Confidence < SafeConfidenceThreshold {
		out.Status = StatusReview
		out.Reasons = append(out.Reasons, ReasonInsufficientEvidence)
		out.ReasonDetails = append(out.ReasonDetails, ReasonDetail{
			Code:    ReasonInsufficientEvidence,
			Message: "SAFE requires high confidence; downgraded to REVIEW",
		})
	}
	out.Summary = summarize(out, ctx)
	return out
}

type evalResult struct {
	status     BranchStatus
	confidence int
	reasons    []Reason
	details    []ReasonDetail
}

func (a Analyzer) evaluate(ctx BranchContext) evalResult {
	if r := protection(ctx); r != nil {
		return *r
	}

	if open := firstOpenPR(ctx.PullRequests); open != nil {
		code := ReasonPullRequestOpen
		if open.Draft || open.State == PRDraft {
			code = ReasonPullRequestDraft
		}
		return keep(100, []Reason{code}, []ReasonDetail{{
			Code:    code,
			Message: fmt.Sprintf("PR #%d is %s", open.Number, open.State),
		}})
	}

	if ctx.MergedPR != nil {
		return a.evaluateMergedPR(ctx)
	}

	if closed := firstClosedUnmerged(ctx.PullRequests); closed != nil {
		reasons := []Reason{ReasonPullRequestClosed, ReasonClosedUnmerged}
		if len(ctx.LocalOnlyCommits) > 0 {
			reasons = append(reasons, ReasonLocalOnlyCommits)
			return keep(95, reasons, []ReasonDetail{{
				Code:    ReasonClosedUnmerged,
				Message: fmt.Sprintf("PR #%d closed without merge and unique local commits exist", closed.Number),
			}})
		}
		if ctx.IsMergedToTrunk {
			return a.maybeRemoteGate(ctx, safeMergedTrunk(ctx, reasons...))
		}
		reasons = append(reasons, ReasonInsufficientEvidence)
		return review(70, reasons, nil)
	}

	return a.evaluateGitOnly(ctx)
}

func (a Analyzer) evaluateMergedPR(ctx BranchContext) evalResult {
	pr := ctx.MergedPR
	base := []Reason{ReasonPullRequestMerged}
	details := []ReasonDetail{{
		Code:    ReasonPullRequestMerged,
		Message: fmt.Sprintf("PR #%d was merged", pr.Number),
	}}

	switch ctx.PRHeadRelation {
	case RelAhead:
		base = append(base, ReasonLocalHeadAheadPR, ReasonLocalCommitsAfterMergedPR)
		return keep(100, base, append(details, ReasonDetail{
			Code:    ReasonLocalCommitsAfterMergedPR,
			Message: fmt.Sprintf("Local HEAD moved after merged PR head %s", shortSHA(pr.HeadSHA)),
		}))
	case RelDiverged:
		base = append(base, ReasonLocalHeadDivergedFromPR)
		return keep(100, base, append(details, ReasonDetail{
			Code:    ReasonLocalHeadDivergedFromPR,
			Message: "Local branch diverged from the merged PR head",
		}))
	case RelUnknown:
		base = append(base, ReasonInsufficientEvidence)
		return review(50, base, details)
	case RelEqual:
		base = append(base, ReasonLocalHeadMatchesPR)
		if !ctx.IsMergedToTrunk {
			base = append(base, ReasonSquashMerged)
		}
	case RelBehind:
		base = append(base, ReasonLocalHeadBehindPR)
	}

	if len(ctx.LocalOnlyCommits) == 0 {
		base = append(base, ReasonNoLocalOnlyCommits)
	}
	base = appendRemoteReason(base, ctx)
	return a.maybeRemoteGate(ctx, safe(95, base, details))
}

func (a Analyzer) evaluateGitOnly(ctx BranchContext) evalResult {
	reasons := make([]Reason, 0, 6)
	if ctx.ProviderError != "" {
		reasons = append(reasons, ReasonProviderUnavailable)
	}
	if ctx.Offline {
		reasons = append(reasons, ReasonProviderUnavailable)
	}
	if ctx.Upstream == "" {
		reasons = append(reasons, ReasonNoUpstream)
	}
	reasons = appendRemoteReason(reasons, ctx)

	if ctx.IsMergedToTrunk {
		if !ctx.DefaultKnown {
			reasons = append(reasons, ReasonDefaultBranchUnknown, ReasonInsufficientEvidence)
			return review(50, reasons, nil)
		}
		reasons = append(reasons, ReasonMergedIntoTrunk)
		if len(ctx.LocalOnlyCommits) == 0 {
			reasons = append(reasons, ReasonNoLocalOnlyCommits)
		}
		return a.maybeRemoteGate(ctx, safe(95, reasons, []ReasonDetail{{
			Code:    ReasonMergedIntoTrunk,
			Message: fmt.Sprintf("Fully contained in %s", ctx.DefaultBranch),
		}}))
	}

	if ctx.DefaultKnown {
		reasons = append(reasons, ReasonNotMergedIntoTrunk)
	}

	if !ctx.DefaultKnown && len(ctx.LocalOnlyCommits) > 0 && !(ctx.RemoteState == RemoteDeleted && ctx.LastKnownRemoteSHA != "" && ctx.LastKnownRemoteSHA == ctx.LocalSHA) {
		reasons = append(reasons, ReasonDefaultBranchUnknown, ReasonInsufficientEvidence)
		return review(50, reasons, nil)
	}

	if len(ctx.LocalOnlyCommits) == 0 {
		reasons = append(reasons, ReasonNoLocalOnlyCommits, ReasonMergedIntoRemote)
		return a.maybeRemoteGate(ctx, safe(90, reasons, []ReasonDetail{{
			Code:    ReasonMergedIntoRemote,
			Message: "All commits are reachable from another remote branch",
		}}))
	}

	if ctx.RemoteState == RemoteDeleted && ctx.LastKnownRemoteSHA != "" && ctx.LastKnownRemoteSHA == ctx.LocalSHA {
		reasons = append(reasons, ReasonAbandonedRemoteDeleted, ReasonLocalMatchesLastRemote)
		return safe(90, reasons, []ReasonDetail{{
			Code:    ReasonAbandonedRemoteDeleted,
			Message: "Remote branch was deleted and local HEAD still matches the last pushed SHA",
		}})
	}

	reasons = append(reasons, ReasonLocalOnlyCommits)
	if ctx.RemoteState == RemoteDeleted {
		return keep(90, reasons, []ReasonDetail{{
			Code:    ReasonLocalOnlyCommits,
			Message: fmt.Sprintf("%d commit(s) are not reachable from any remote", len(ctx.LocalOnlyCommits)),
		}})
	}
	return keep(85, reasons, nil)
}

func (a Analyzer) maybeRemoteGate(ctx BranchContext, candidate evalResult) evalResult {
	if candidate.status != StatusSafe {
		return candidate
	}
	if !a.RequireRemoteDeleted {
		return candidate
	}
	if ctx.RemoteState == RemoteExists {
		candidate.status = StatusReview
		candidate.confidence = 80
		if !hasReason(candidate.reasons, ReasonRemoteBranchExists) {
			candidate.reasons = append(candidate.reasons, ReasonRemoteBranchExists)
		}
		candidate.details = append(candidate.details, ReasonDetail{
			Code:    ReasonRemoteBranchExists,
			Message: "Remote branch still exists; not selected for automatic cleanup",
		})
		return candidate
	}
	if ctx.RemoteState == RemoteUnknown && !ctx.IsMergedToTrunk {
		candidate.status = StatusReview
		candidate.confidence = 70
		if !hasReason(candidate.reasons, ReasonRemoteStateUnknown) {
			candidate.reasons = append(candidate.reasons, ReasonRemoteStateUnknown)
		}
		candidate.details = append(candidate.details, ReasonDetail{
			Code:    ReasonRemoteStateUnknown,
			Message: "Remote branch state could not be determined",
		})
		return candidate
	}
	return candidate
}

func protection(ctx BranchContext) *evalResult {
	if ctx.IsCurrent {
		r := protected(ReasonBranchIsCurrent, "Branch is currently checked out")
		return &r
	}
	if ctx.IsDefault {
		r := protected(ReasonBranchIsDefault, "Branch is the repository default branch")
		return &r
	}
	if ctx.InWorktree {
		r := protected(ReasonBranchInWorktree, "Branch is checked out in another worktree")
		return &r
	}
	if ctx.IsProtected {
		msg := "Branch matches a protected pattern"
		if ctx.ProtectedPattern != "" {
			msg = fmt.Sprintf("Branch matches protected pattern %q", ctx.ProtectedPattern)
		}
		r := protected(ReasonBranchMatchesProtected, msg)
		return &r
	}
	return nil
}

func firstOpenPR(prs []PullRequest) *PullRequest {
	for i := range prs {
		if prs[i].State == PROpen || prs[i].State == PRDraft || prs[i].Draft {
			return &prs[i]
		}
	}
	return nil
}

func firstClosedUnmerged(prs []PullRequest) *PullRequest {
	for i := range prs {
		if prs[i].State == PRClosed && !prs[i].Merged {
			return &prs[i]
		}
	}
	return nil
}

func appendRemoteReason(reasons []Reason, ctx BranchContext) []Reason {
	switch ctx.RemoteState {
	case RemoteDeleted:
		return append(reasons, ReasonRemoteBranchDeleted)
	case RemoteExists:
		return append(reasons, ReasonRemoteBranchExists)
	case RemoteUnknown:
		return append(reasons, ReasonRemoteStateUnknown)
	}
	return reasons
}

func hasReason(reasons []Reason, code Reason) bool {
	for _, r := range reasons {
		if r == code {
			return true
		}
	}
	return false
}

func protected(code Reason, msg string) evalResult {
	return evalResult{
		status:     StatusProtected,
		confidence: 100,
		reasons:    []Reason{code},
		details:    []ReasonDetail{{Code: code, Message: msg}},
	}
}

func keep(confidence int, reasons []Reason, details []ReasonDetail) evalResult {
	return evalResult{status: StatusKeep, confidence: confidence, reasons: reasons, details: details}
}

func review(confidence int, reasons []Reason, details []ReasonDetail) evalResult {
	return evalResult{status: StatusReview, confidence: confidence, reasons: reasons, details: details}
}

func safe(confidence int, reasons []Reason, details []ReasonDetail) evalResult {
	return evalResult{status: StatusSafe, confidence: confidence, reasons: reasons, details: details}
}

func safeMergedTrunk(ctx BranchContext, extra ...Reason) evalResult {
	reasons := append([]Reason{ReasonMergedIntoTrunk}, extra...)
	return safe(95, reasons, []ReasonDetail{{
		Code:    ReasonMergedIntoTrunk,
		Message: fmt.Sprintf("Fully contained in %s", ctx.DefaultBranch),
	}})
}

func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

func summarize(a BranchAnalysis, ctx BranchContext) string {
	parts := make([]string, 0, 4)
	if a.HasReason(ReasonPullRequestMerged) && ctx.MergedPR != nil {
		label := fmt.Sprintf("PR #%d merged", ctx.MergedPR.Number)
		if a.HasReason(ReasonSquashMerged) {
			label = fmt.Sprintf("PR #%d squash-merged", ctx.MergedPR.Number)
		}
		parts = append(parts, label)
	}
	if a.HasReason(ReasonMergedIntoTrunk) && ctx.DefaultBranch != "" && !a.HasReason(ReasonPullRequestMerged) {
		parts = append(parts, fmt.Sprintf("Fully merged into %s", ctx.DefaultBranch))
	}
	if a.HasReason(ReasonMergedIntoRemote) && !a.HasReason(ReasonMergedIntoTrunk) {
		parts = append(parts, "reachable from another remote")
	}
	if a.HasReason(ReasonAbandonedRemoteDeleted) {
		parts = append(parts, "abandoned after remote delete")
	}
	if a.HasReason(ReasonRemoteBranchDeleted) {
		parts = append(parts, "remote deleted")
	}
	if a.HasReason(ReasonRemoteBranchExists) {
		parts = append(parts, "remote still exists")
	}
	if a.HasReason(ReasonLocalHeadMatchesPR) {
		parts = append(parts, "local HEAD matches PR head")
	}
	if a.HasReason(ReasonLocalCommitsAfterMergedPR) {
		n := len(ctx.LocalOnlyCommits)
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d commits exist only locally", n))
		} else {
			parts = append(parts, "local branch contains commits after the merged PR")
		}
	}
	if a.HasReason(ReasonLocalHeadDivergedFromPR) {
		parts = append(parts, "local branch diverged from PR head")
	}
	if a.HasReason(ReasonPullRequestOpen) || a.HasReason(ReasonPullRequestDraft) {
		if ctx.EvidencePR() != nil {
			parts = append(parts, fmt.Sprintf("PR #%d is still open", ctx.EvidencePR().Number))
		} else {
			parts = append(parts, "PR is still open")
		}
	}
	if a.HasReason(ReasonLocalOnlyCommits) && !a.HasReason(ReasonLocalCommitsAfterMergedPR) {
		parts = append(parts, fmt.Sprintf("%d commits exist only locally", len(ctx.LocalOnlyCommits)))
	}
	if a.HasReason(ReasonInsufficientEvidence) && len(parts) == 0 {
		parts = append(parts, "merge state unknown")
	}
	if a.HasReason(ReasonNoPullRequest) || (a.HasReason(ReasonRemoteBranchDeleted) && ctx.MergedPR == nil && !a.HasReason(ReasonMergedIntoTrunk) && a.Status == StatusReview) {
		if !containsString(parts, "merge state unknown") && ctx.MergedPR == nil && !a.HasReason(ReasonMergedIntoTrunk) {
			if !containsString(parts, "no merged PR found") {
				parts = append(parts, "no merged PR found")
			}
		}
	}
	if len(parts) == 0 {
		switch a.Status {
		case StatusProtected:
			if a.HasReason(ReasonBranchIsCurrent) {
				return "currently checked out"
			}
			if a.HasReason(ReasonBranchIsDefault) {
				return "default branch"
			}
			if a.HasReason(ReasonBranchInWorktree) {
				return "checked out in a worktree"
			}
			if ctx.ProtectedPattern != "" {
				return ctx.ProtectedPattern
			}
			return "protected"
		case StatusSafe:
			return "no local changes"
		default:
			return "needs review"
		}
	}
	return strings.Join(parts, " · ")
}

func (ctx BranchContext) EvidencePR() *PullRequest {
	if ctx.MergedPR != nil {
		return ctx.MergedPR
	}
	return firstOpenPR(ctx.PullRequests)
}

func containsString(items []string, s string) bool {
	for _, i := range items {
		if i == s {
			return true
		}
	}
	return false
}
