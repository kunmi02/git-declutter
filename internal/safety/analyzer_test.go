package safety

import (
	"testing"
	"time"
)

func TestAnalyzer_ProtectionRules(t *testing.T) {
	a := NewAnalyzer(true)
	cases := []struct {
		name   string
		ctx    BranchContext
		status BranchStatus
		reason Reason
	}{
		{
			name:   "current branch",
			ctx:    BranchContext{Name: "feature/x", LocalSHA: "aaa", IsCurrent: true},
			status: StatusProtected,
			reason: ReasonBranchIsCurrent,
		},
		{
			name:   "default branch",
			ctx:    BranchContext{Name: "main", LocalSHA: "aaa", IsDefault: true, DefaultKnown: true, DefaultBranch: "main"},
			status: StatusProtected,
			reason: ReasonBranchIsDefault,
		},
		{
			name:   "worktree branch",
			ctx:    BranchContext{Name: "feature/x", LocalSHA: "aaa", InWorktree: true},
			status: StatusProtected,
			reason: ReasonBranchInWorktree,
		},
		{
			name:   "protected pattern",
			ctx:    BranchContext{Name: "release/1.0", LocalSHA: "aaa", IsProtected: true, ProtectedPattern: "release/*"},
			status: StatusProtected,
			reason: ReasonBranchMatchesProtected,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a.Analyze(tc.ctx)
			if got.Status != tc.status {
				t.Fatalf("status = %s, want %s (reasons=%v)", got.Status, tc.status, got.Reasons)
			}
			if !got.HasReason(tc.reason) {
				t.Fatalf("missing reason %s in %v", tc.reason, got.Reasons)
			}
			if _, ok := got.AsSafe(); ok {
				t.Fatal("protected branch must not convert to SafeBranch")
			}
		})
	}
}

func TestAnalyzer_OpenAndDraftPR(t *testing.T) {
	a := NewAnalyzer(true)
	open := a.Analyze(BranchContext{
		Name:     "feature/x",
		LocalSHA: "aaa",
		PullRequests: []PullRequest{{
			Number: 12, State: PROpen, HeadSHA: "aaa", HeadBranch: "feature/x",
		}},
	})
	if open.Status != StatusKeep || !open.HasReason(ReasonPullRequestOpen) {
		t.Fatalf("open PR: %+v", open)
	}

	draft := a.Analyze(BranchContext{
		Name:     "feature/x",
		LocalSHA: "aaa",
		PullRequests: []PullRequest{{
			Number: 13, State: PRDraft, Draft: true, HeadSHA: "aaa",
		}},
	})
	if draft.Status != StatusKeep || !draft.HasReason(ReasonPullRequestDraft) {
		t.Fatalf("draft PR: %+v", draft)
	}
}

func TestAnalyzer_MergedPRHeadRelations(t *testing.T) {
	a := NewAnalyzer(true)
	mergedAt := time.Now().Add(-18 * 24 * time.Hour)
	pr := &PullRequest{
		Number: 481, State: PRMerged, Merged: true, HeadSHA: "ccc",
		BaseBranch: "main", MergedAt: &mergedAt,
	}

	t.Run("local HEAD equals PR HEAD is SAFE when remote deleted", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/payments", LocalSHA: "ccc",
			RemoteState: RemoteDeleted, MergedPR: pr, PRHeadRelation: RelEqual,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonPullRequestMerged) || !got.HasReason(ReasonLocalHeadMatchesPR) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("local HEAD behind PR HEAD is SAFE when remote deleted", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/payments", LocalSHA: "bbb",
			RemoteState: RemoteDeleted, MergedPR: pr, PRHeadRelation: RelBehind,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonLocalHeadBehindPR) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("local commits after merged PR is KEEP", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/payments", LocalSHA: "eee",
			RemoteState: RemoteDeleted, MergedPR: pr, PRHeadRelation: RelAhead,
			LocalOnlyCommits: []Commit{{SHA: "ddd"}, {SHA: "eee"}},
			DefaultKnown:     true, DefaultBranch: "main",
		})
		if got.Status != StatusKeep {
			t.Fatalf("status=%s reasons=%v (critical regression)", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonLocalCommitsAfterMergedPR) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
		if _, ok := got.AsSafe(); ok {
			t.Fatal("KEEP must never be SafeBranch")
		}
	})

	t.Run("diverged local branch is KEEP", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/payments", LocalSHA: "fff",
			RemoteState: RemoteDeleted, MergedPR: pr, PRHeadRelation: RelDiverged,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusKeep {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonLocalHeadDivergedFromPR) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("unknown relation is REVIEW", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/payments", LocalSHA: "zzz",
			RemoteState: RemoteDeleted, MergedPR: pr, PRHeadRelation: RelUnknown,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusReview {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
	})
}

func TestAnalyzer_SquashMerge(t *testing.T) {
	a := NewAnalyzer(true)
	pr := &PullRequest{Number: 407, State: PRMerged, Merged: true, HeadSHA: "ddd"}
	got := a.Analyze(BranchContext{
		Name: "feature/invoice-export", LocalSHA: "ddd",
		RemoteState: RemoteDeleted, MergedPR: pr, PRHeadRelation: RelEqual,
		IsMergedToTrunk:  false,
		LocalOnlyCommits: []Commit{{SHA: "aaa"}, {SHA: "bbb"}, {SHA: "ccc"}, {SHA: "ddd"}},
		DefaultKnown:     true, DefaultBranch: "main",
	})
	if got.Status != StatusSafe {
		t.Fatalf("squash merge should be SAFE when HEAD matches PR head, got %s %v", got.Status, got.Reasons)
	}
	if !got.HasReason(ReasonSquashMerged) {
		t.Fatalf("expected squash_merged, reasons=%v", got.Reasons)
	}
}

func TestAnalyzer_GitMergeAndRemote(t *testing.T) {
	a := NewAnalyzer(true)

	t.Run("fully merged into trunk and remote deleted is SAFE", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "fix/payment-timeout", LocalSHA: "abc",
			RemoteState: RemoteDeleted, IsMergedToTrunk: true,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonMergedIntoTrunk) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("fully merged but remote still exists is REVIEW", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "fix/payment-timeout", LocalSHA: "abc",
			RemoteState: RemoteExists, IsMergedToTrunk: true,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusReview {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonRemoteBranchExists) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("unmerged with unique commits and remote deleted is KEEP", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/refunds", LocalSHA: "abc",
			RemoteState: RemoteDeleted, IsMergedToTrunk: false,
			LocalOnlyCommits: []Commit{{SHA: "1"}, {SHA: "2"}, {SHA: "3"}},
			DefaultKnown:     true, DefaultBranch: "main",
		})
		if got.Status != StatusKeep {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonLocalOnlyCommits) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("reachable from another remote and remote deleted is SAFE", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feat/into-develop", LocalSHA: "abc",
			RemoteState: RemoteDeleted, IsMergedToTrunk: false,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonMergedIntoRemote) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("reachable from another remote but feature remote still exists is REVIEW", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feat/into-develop", LocalSHA: "abc",
			RemoteState: RemoteExists, IsMergedToTrunk: false,
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusReview {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
	})

	t.Run("abandoned after remote delete matching last push is SAFE", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feat/try-out", LocalSHA: "pushedsha",
			RemoteState:        RemoteDeleted,
			IsMergedToTrunk:    false,
			LocalOnlyCommits:   []Commit{{SHA: "pushedsha"}},
			LastKnownRemoteSHA: "pushedsha",
			DefaultKnown:       true,
			DefaultBranch:      "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonAbandonedRemoteDeleted) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("local commits after last remote SHA stay KEEP", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feat/try-out", LocalSHA: "newer",
			RemoteState:        RemoteDeleted,
			IsMergedToTrunk:    false,
			LocalOnlyCommits:   []Commit{{SHA: "pushedsha"}, {SHA: "newer"}},
			LastKnownRemoteSHA: "pushedsha",
			DefaultKnown:       true,
			DefaultBranch:      "main",
		})
		if got.Status != StatusKeep {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
	})

	t.Run("remote unknown is never treated as deleted", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/x", LocalSHA: "abc",
			RemoteState: RemoteUnknown, IsMergedToTrunk: false,
			MergedPR:       &PullRequest{Number: 1, State: PRMerged, Merged: true, HeadSHA: "abc"},
			PRHeadRelation: RelEqual,
			DefaultKnown:   true, DefaultBranch: "main",
		})
		if got.Status != StatusReview {
			t.Fatalf("unknown remote must not be SAFE, got %s %v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonRemoteStateUnknown) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})
}

func TestAnalyzer_ClosedPRAndProvider(t *testing.T) {
	a := NewAnalyzer(true)

	t.Run("closed unmerged with unique commits is KEEP", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/x", LocalSHA: "abc",
			RemoteState:      RemoteExists,
			PullRequests:     []PullRequest{{Number: 9, State: PRClosed, Merged: false, HeadSHA: "abc"}},
			LocalOnlyCommits: []Commit{{SHA: "abc"}},
			DefaultKnown:     true, DefaultBranch: "main",
		})
		if got.Status != StatusKeep {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonClosedUnmerged) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("closed unmerged but fully merged into trunk can be SAFE", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/x", LocalSHA: "abc",
			RemoteState: RemoteDeleted, IsMergedToTrunk: true,
			PullRequests: []PullRequest{{Number: 9, State: PRClosed, Merged: false, HeadSHA: "abc"}},
			DefaultKnown: true, DefaultBranch: "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
	})

	t.Run("provider unavailable falls back to git evidence", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "fix/x", LocalSHA: "abc",
			RemoteState: RemoteDeleted, IsMergedToTrunk: true,
			ProviderError: "github down",
			DefaultKnown:  true, DefaultBranch: "main",
		})
		if got.Status != StatusSafe {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
		if !got.HasReason(ReasonProviderUnavailable) {
			t.Fatalf("reasons=%v", got.Reasons)
		}
	})

	t.Run("insufficient evidence is REVIEW", func(t *testing.T) {
		got := a.Analyze(BranchContext{
			Name: "feature/x", LocalSHA: "abc",
			RemoteState: RemoteUnknown, DefaultKnown: false,
		})
		if got.Status != StatusReview {
			t.Fatalf("status=%s reasons=%v", got.Status, got.Reasons)
		}
	})
}

func TestAnalyzer_MultiplePRs(t *testing.T) {
	a := NewAnalyzer(true)
	old := time.Now().Add(-40 * 24 * time.Hour)
	newer := time.Now().Add(-2 * 24 * time.Hour)
	got := a.Analyze(BranchContext{
		Name: "feature/x", LocalSHA: "newhead",
		RemoteState: RemoteDeleted,
		PullRequests: []PullRequest{
			{Number: 1, State: PRMerged, Merged: true, HeadSHA: "oldhead", MergedAt: &old},
			{Number: 2, State: PRMerged, Merged: true, HeadSHA: "oldhead2", MergedAt: &newer},
			{Number: 3, State: PROpen, HeadSHA: "newhead"},
		},
	})
	if got.Status != StatusKeep {
		t.Fatalf("open PR among multiple must KEEP, got %s %v", got.Status, got.Reasons)
	}
	if !got.HasReason(ReasonPullRequestOpen) {
		t.Fatalf("reasons=%v", got.Reasons)
	}
}

func TestAnalyzer_MostRecentMergedPRAhead(t *testing.T) {
	a := NewAnalyzer(true)
	old := time.Now().Add(-40 * 24 * time.Hour)
	newer := time.Now().Add(-2 * 24 * time.Hour)
	got := a.Analyze(BranchContext{
		Name: "feature/x", LocalSHA: "localextra",
		RemoteState: RemoteDeleted,
		PullRequests: []PullRequest{
			{Number: 1, State: PRMerged, Merged: true, HeadSHA: "old", MergedAt: &old},
			{Number: 2, State: PRMerged, Merged: true, HeadSHA: "mergedhead", MergedAt: &newer},
		},
		MergedPR:       &PullRequest{Number: 2, State: PRMerged, Merged: true, HeadSHA: "mergedhead", MergedAt: &newer},
		PRHeadRelation: RelAhead,
		DefaultKnown:   true, DefaultBranch: "main",
	})
	if got.Status != StatusKeep {
		t.Fatalf("moved after most recent merged PR must KEEP, got %s %v", got.Status, got.Reasons)
	}
}

func TestAnalyzer_CleanupRemoteExistingConfig(t *testing.T) {
	a := NewAnalyzer(false)
	got := a.Analyze(BranchContext{
		Name: "feature/x", LocalSHA: "abc",
		RemoteState: RemoteExists, IsMergedToTrunk: true,
		DefaultKnown: true, DefaultBranch: "main",
	})
	if got.Status != StatusSafe {
		t.Fatalf("requireRemoteDeleted=false should allow SAFE, got %s %v", got.Status, got.Reasons)
	}
}

func TestSafeBranchInvariant(t *testing.T) {
	a := NewAnalyzer(true)
	for _, st := range []BranchContext{
		{Name: "k", LocalSHA: "x", IsCurrent: true},
		{Name: "k", LocalSHA: "x", LocalOnlyCommits: []Commit{{SHA: "x"}}, DefaultKnown: true, DefaultBranch: "main", RemoteState: RemoteDeleted},
		{Name: "k", LocalSHA: "x", DefaultKnown: true, DefaultBranch: "main", RemoteState: RemoteDeleted},
	} {
		got := a.Analyze(st)
		if got.Status == StatusSafe {
			continue
		}
		if _, ok := got.AsSafe(); ok {
			t.Fatalf("status %s must not yield SafeBranch", got.Status)
		}
	}
}

func TestEveryAnalysisHasAReason(t *testing.T) {
	a := NewAnalyzer(true)
	got := a.Analyze(BranchContext{Name: "x", LocalSHA: "y"})
	if len(got.Reasons) == 0 {
		t.Fatal("expected at least one reason")
	}
}
