package safety

// Reason is a structured, stable code explaining a classification.
type Reason string

const (
	ReasonBranchIsCurrent           Reason = "branch_is_current"
	ReasonBranchIsDefault           Reason = "branch_is_default"
	ReasonBranchInWorktree          Reason = "branch_in_worktree"
	ReasonBranchMatchesProtected    Reason = "branch_matches_protected_pattern"
	ReasonMergedIntoTrunk           Reason = "merged_into_trunk"
	ReasonMergedIntoRemote          Reason = "merged_into_remote"
	ReasonNotMergedIntoTrunk        Reason = "not_merged_into_trunk"
	ReasonAbandonedRemoteDeleted    Reason = "abandoned_remote_deleted"
	ReasonLocalMatchesLastRemote    Reason = "local_matches_last_remote"
	ReasonRemoteBranchDeleted       Reason = "remote_branch_deleted"
	ReasonRemoteBranchExists        Reason = "remote_branch_exists"
	ReasonRemoteStateUnknown        Reason = "remote_state_unknown"
	ReasonPullRequestOpen           Reason = "pull_request_open"
	ReasonPullRequestClosed         Reason = "pull_request_closed"
	ReasonPullRequestMerged         Reason = "pull_request_merged"
	ReasonPullRequestDraft          Reason = "pull_request_draft"
	ReasonLocalHeadMatchesPR        Reason = "local_head_matches_pr"
	ReasonLocalHeadBehindPR         Reason = "local_head_behind_pr"
	ReasonLocalHeadAheadPR          Reason = "local_head_ahead_of_pr"
	ReasonLocalHeadDivergedFromPR   Reason = "local_head_diverged_from_pr"
	ReasonLocalCommitsAfterMergedPR Reason = "local_commits_after_merged_pr"
	ReasonLocalOnlyCommits          Reason = "local_only_commits"
	ReasonNoLocalOnlyCommits        Reason = "no_local_only_commits"
	ReasonProviderUnavailable       Reason = "provider_unavailable"
	ReasonProviderAuthRequired      Reason = "provider_auth_required"
	ReasonDefaultBranchUnknown      Reason = "default_branch_unknown"
	ReasonNoUpstream                Reason = "no_upstream"
	ReasonInsufficientEvidence      Reason = "insufficient_evidence"
	ReasonSquashMerged              Reason = "squash_merged"
	ReasonClosedUnmerged            Reason = "pull_request_closed_unmerged"
	ReasonNoPullRequest             Reason = "no_pull_request"
)

// ReasonDetail is a reason plus optional human-readable context.
type ReasonDetail struct {
	Code    Reason `json:"code"`
	Message string `json:"message,omitempty"`
}

func (r Reason) Positive() bool {
	switch r {
	case ReasonMergedIntoTrunk,
		ReasonMergedIntoRemote,
		ReasonRemoteBranchDeleted,
		ReasonPullRequestMerged,
		ReasonLocalHeadMatchesPR,
		ReasonLocalHeadBehindPR,
		ReasonNoLocalOnlyCommits,
		ReasonSquashMerged,
		ReasonAbandonedRemoteDeleted,
		ReasonLocalMatchesLastRemote:
		return true
	default:
		return false
	}
}
