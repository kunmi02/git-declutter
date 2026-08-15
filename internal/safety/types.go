package safety

import "time"

type RemoteState string

const (
	RemoteExists  RemoteState = "exists"
	RemoteDeleted RemoteState = "deleted"
	RemoteUnknown RemoteState = "unknown"
)

type HeadRelation string

const (
	RelEqual    HeadRelation = "equal"
	RelBehind   HeadRelation = "behind"
	RelAhead    HeadRelation = "ahead"
	RelDiverged HeadRelation = "diverged"
	RelUnknown  HeadRelation = "unknown"
	RelNone     HeadRelation = "none"
)

type PRState string

const (
	PROpen   PRState = "open"
	PRDraft  PRState = "draft"
	PRClosed PRState = "closed"
	PRMerged PRState = "merged"
)

type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject,omitempty"`
}

type PullRequest struct {
	Number     int        `json:"number"`
	State      PRState    `json:"state"`
	Title      string     `json:"title,omitempty"`
	HeadSHA    string     `json:"headSha,omitempty"`
	HeadBranch string     `json:"headBranch,omitempty"`
	BaseBranch string     `json:"baseBranch,omitempty"`
	MergedAt   *time.Time `json:"mergedAt,omitempty"`
	URL        string     `json:"url,omitempty"`
	Merged     bool       `json:"merged"`
	Draft      bool       `json:"draft"`
}

type BranchContext struct {
	Name               string
	LocalSHA           string
	Upstream           string
	RemoteName         string
	RemoteState        RemoteState
	IsCurrent          bool
	InWorktree         bool
	IsProtected        bool
	ProtectedPattern   string
	IsDefault          bool
	IsMergedToTrunk    bool
	DefaultKnown       bool
	DefaultBranch      string
	LocalOnlyCommits   []Commit
	LastKnownRemoteSHA string
	PullRequests       []PullRequest
	MergedPR           *PullRequest
	PRHeadRelation     HeadRelation
	ProviderError      string
	Offline            bool
	AnalyzedAt         time.Time
}

type Evidence struct {
	LocalSHA            string       `json:"localSha"`
	DefaultBranch       string       `json:"defaultBranch,omitempty"`
	RemoteState         RemoteState  `json:"remoteState"`
	MergedIntoTrunk     bool         `json:"mergedIntoTrunk"`
	LastKnownRemoteSHA  string       `json:"lastKnownRemoteSha,omitempty"`
	LocalOnlyCommitSHAs []string     `json:"localOnlyCommitShas,omitempty"`
	PullRequest         *PullRequest `json:"pullRequest,omitempty"`
	PRHeadRelation      HeadRelation `json:"prHeadRelation,omitempty"`
	Worktree            bool         `json:"worktree"`
	Protected           bool         `json:"protected"`
}

type BranchAnalysis struct {
	Branch        string         `json:"name"`
	SHA           string         `json:"sha"`
	Status        BranchStatus   `json:"status"`
	Confidence    int            `json:"-"`
	Reasons       []Reason       `json:"reasons"`
	ReasonDetails []ReasonDetail `json:"reasonDetails,omitempty"`
	Evidence      Evidence       `json:"evidence"`
	PullRequest   *PullRequest   `json:"pullRequest,omitempty"`
	AnalyzedAt    time.Time      `json:"analyzedAt"`
	Summary       string         `json:"summary,omitempty"`
}

// SafeBranch is a branch that has been classified SAFE.
// Deletion APIs should accept this type rather than a raw branch name
// so KEEP / REVIEW / PROTECTED cannot enter the automatic deletion path.
type SafeBranch struct {
	Analysis BranchAnalysis
}

func (a BranchAnalysis) AsSafe() (SafeBranch, bool) {
	if a.Status != StatusSafe {
		return SafeBranch{}, false
	}
	return SafeBranch{Analysis: a}, true
}

func (a BranchAnalysis) HasReason(code Reason) bool {
	for _, r := range a.Reasons {
		if r == code {
			return true
		}
	}
	return false
}
