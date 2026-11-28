package application

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*ProcessApprovalStore, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	clock := &now
	store := NewProcessApprovalStore(10 * time.Minute)
	store.SetClock(func() time.Time { return *clock })
	return store, clock
}

func mustCreate(t *testing.T, store *ProcessApprovalStore, command string, args []string) *ProcessApproval {
	t.Helper()
	approval, err := store.Create("sess-1", "dev-1", "/workspace", command, args, "gradle executes project build logic")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	return approval
}

// TestProcessApprovalStore_CreateGrantsNothing is the regression test for the MP26.1 self-approval
// flaw: requesting an approval must not produce any execution capability.
func TestProcessApprovalStore_CreateGrantsNothing(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"assembleDebug"})

	if approval.State != ProcessApprovalPending {
		t.Fatalf("expected pending state, got %q", approval.State)
	}
	if approval.Token != "" {
		t.Fatalf("a pending proposal must not carry an execution token, got %q", approval.Token)
	}

	// Without a decision, no token exists, so nothing can be consumed.
	if _, err := store.Consume("", "sess-1", "dev-1", "/workspace", "gradlew", []string{"assembleDebug"}); err == nil {
		t.Fatalf("expected empty token to be rejected")
	}
}

func TestProcessApprovalStore_DigestBindingIsUnambiguous(t *testing.T) {
	// Concatenation ambiguity must not let one approval cover a different argument vector.
	cases := [][2]string{
		{"a b", ""},
		{"a", "b"},
	}
	first := CanonicalCommandDigest("/ws", "gradlew", []string{cases[0][0], cases[0][1]})
	second := CanonicalCommandDigest("/ws", "gradlew", []string{cases[1][0]})
	if first == second {
		t.Fatalf("expected distinct digests for [\"a b\",\"\"] and [\"a\",\"b\"]")
	}
	if CanonicalCommandDigest("/ws", "gradlew", nil) == CanonicalCommandDigest("/ws", "gradlew", []string{}) {
		// nil and empty must be treated identically: both mean "no arguments".
		return
	}
	if CanonicalCommandDigest("/ws1", "gradlew", []string{"build"}) == CanonicalCommandDigest("/ws2", "gradlew", []string{"build"}) {
		t.Fatalf("expected workspace to be part of the digest")
	}
}

func TestProcessApprovalStore_ApproveRequiresMatchingDigest(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	if _, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", "not-the-digest"); !errors.Is(err, ErrProcessApprovalDigest) {
		t.Fatalf("expected digest mismatch rejection, got %v", err)
	}

	// A rejected decision attempt must not change state: still pending, still no token.
	current, err := store.Get(approval.ID, "sess-1", "dev-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if current.State != ProcessApprovalPending || current.Token != "" {
		t.Fatalf("expected state to remain pending without a token, got %q / %q", current.State, current.Token)
	}
}

func TestProcessApprovalStore_ApproveIssuesSingleUseToken(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	decided, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest)
	if err != nil {
		t.Fatalf("Decide(approve) failed: %v", err)
	}
	if decided.State != ProcessApprovalApproved || decided.Token == "" {
		t.Fatalf("expected approved state with a token, got %q / %q", decided.State, decided.Token)
	}

	if _, err := store.Consume(decided.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"build"}); err != nil {
		t.Fatalf("first consumption failed: %v", err)
	}

	// Replay must fail.
	if _, err := store.Consume(decided.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"build"}); !errors.Is(err, ErrProcessApprovalToken) {
		t.Fatalf("expected replay rejection, got %v", err)
	}
}

func TestProcessApprovalStore_RejectPreventsExecution(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	rejected, err := store.Decide(approval.ID, "sess-1", "dev-1", "reject", "")
	if err != nil {
		t.Fatalf("Decide(reject) failed: %v", err)
	}
	if rejected.State != ProcessApprovalRejected {
		t.Fatalf("expected rejected state, got %q", rejected.State)
	}
	if rejected.Token != "" {
		t.Fatalf("a rejected approval must never carry a token")
	}

	// A second decision is refused, so a reject cannot be flipped to an approve.
	if _, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest); !errors.Is(err, ErrProcessApprovalNotPending) {
		t.Fatalf("expected not-pending rejection, got %v", err)
	}
}

func TestProcessApprovalStore_ConsumeRevalidatesScope(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		workspace string
		command   string
		args      []string
		wantErr   error
	}{
		{"different arguments", "sess-1", "/workspace", "gradlew", []string{"build", "--offline"}, ErrProcessApprovalScope},
		{"different command", "sess-1", "/workspace", "gradle", []string{"build"}, ErrProcessApprovalScope},
		{"different workspace", "sess-1", "/other", "gradlew", []string{"build"}, ErrProcessApprovalScope},
		{"different session", "sess-2", "/workspace", "gradlew", []string{"build"}, ErrProcessApprovalOwner},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newTestStore(t)
			approval := mustCreate(t, store, "gradlew", []string{"build"})
			decided, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest)
			if err != nil {
				t.Fatalf("Decide failed: %v", err)
			}

			_, err = store.Consume(decided.Token, tc.sessionID, "dev-1", tc.workspace, tc.command, tc.args)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestProcessApprovalStore_ExpiryBlocksDecisionAndConsumption(t *testing.T) {
	store, clock := newTestStore(t)

	undecided := mustCreate(t, store, "gradlew", []string{"build"})
	*clock = clock.Add(11 * time.Minute)
	if _, err := store.Decide(undecided.ID, "sess-1", "dev-1", "approve", undecided.Digest); !errors.Is(err, ErrProcessApprovalExpired) {
		t.Fatalf("expected expired rejection for decision, got %v", err)
	}

	// A token issued before expiry must also stop working after expiry.
	*clock = clock.Add(-2 * time.Minute) // back to minute 9
	approved := mustCreate(t, store, "gradlew", []string{"test"})
	decided, err := store.Decide(approved.ID, "sess-1", "dev-1", "approve", approved.Digest)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}
	*clock = clock.Add(11 * time.Minute) // past the 10 minute window opened at creation
	if _, err := store.Consume(decided.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"test"}); !errors.Is(err, ErrProcessApprovalExpired) {
		t.Fatalf("expected expired rejection for consumption, got %v", err)
	}
}

func TestProcessApprovalStore_OwnerCannotBeBypassed(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	if _, err := store.Get(approval.ID, "sess-2", "dev-1"); !errors.Is(err, ErrProcessApprovalOwner) {
		t.Fatalf("expected owner rejection on Get, got %v", err)
	}
	if _, err := store.Decide(approval.ID, "sess-1", "dev-2", "approve", approval.Digest); !errors.Is(err, ErrProcessApprovalOwner) {
		t.Fatalf("expected owner rejection on Decide, got %v", err)
	}
	if _, err := store.Get("pappr-unknown", "sess-1", "dev-1"); !errors.Is(err, ErrProcessApprovalNotFound) {
		t.Fatalf("expected not-found rejection, got %v", err)
	}
}

// TestProcessApprovalStore_ConcurrentConsumeAllowsExactlyOne proves single-use semantics hold under
// contention: only one execution may consume one approval.
func TestProcessApprovalStore_ConcurrentConsumeAllowsExactlyOne(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})
	decided, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}

	var successes int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Consume(decided.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"build"}); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&successes); got != 1 {
		t.Fatalf("expected exactly one successful consumption, got %d", got)
	}
}

// TestProcessApprovalStore_ConcurrentDecideAllowsOneDecision proves a proposal cannot be decided
// twice under contention (for example approve racing with reject).
func TestProcessApprovalStore_ConcurrentDecideAllowsOneDecision(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	var decisions int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		decision := "approve"
		if i%2 == 0 {
			decision = "reject"
		}
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			if _, err := store.Decide(approval.ID, "sess-1", "dev-1", d, approval.Digest); err == nil {
				atomic.AddInt64(&decisions, 1)
			}
		}(decision)
	}
	wg.Wait()

	if got := atomic.LoadInt64(&decisions); got != 1 {
		t.Fatalf("expected exactly one accepted decision, got %d", got)
	}
}

// TestProcessApprovalStore_RejectAfterApproveRemovesToken proves a decision cannot be changed into a
// different one, and that no token survives a later reject attempt.
func TestProcessApprovalStore_RejectAfterApproveRemovesToken(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	decided, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}
	if _, err := store.Decide(approval.ID, "sess-1", "dev-1", "reject", ""); !errors.Is(err, ErrProcessApprovalNotPending) {
		t.Fatalf("expected not-pending rejection, got %v", err)
	}
	// The originally issued token remains the only valid one and is still single-use.
	if _, err := store.Consume(decided.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"build"}); err != nil {
		t.Fatalf("expected issued token to remain valid, got %v", err)
	}
}

// TestProcessApprovalStore_CancelAbandonsPendingProposal proves cancellation has real server-side
// effect: the proposal can no longer be approved and can never execute.
func TestProcessApprovalStore_CancelAbandonsPendingProposal(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	cancelled, err := store.Cancel(approval.ID, "sess-1", "dev-1")
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if cancelled.State != ProcessApprovalCancelled {
		t.Fatalf("expected cancelled state, got %q", cancelled.State)
	}
	if cancelled.Token != "" {
		t.Fatalf("a cancelled proposal must never carry a token")
	}

	if _, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest); !errors.Is(err, ErrProcessApprovalNotPending) {
		t.Fatalf("expected approval of a cancelled proposal to be refused, got %v", err)
	}
	if _, err := store.ConsumeByApprovalID(approval.ID, "sess-1", "dev-1", "/workspace", "gradlew", []string{"build"}); err == nil {
		t.Fatalf("expected execution of a cancelled proposal to be refused")
	}
}

// TestProcessApprovalStore_CancelIsOwnerBoundAndIdempotent proves another principal cannot cancel and
// that repeated cancellation has a deterministic outcome.
func TestProcessApprovalStore_CancelIsOwnerBoundAndIdempotent(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})

	if _, err := store.Cancel(approval.ID, "sess-2", "dev-1"); !errors.Is(err, ErrProcessApprovalOwner) {
		t.Fatalf("expected owner rejection for cancellation, got %v", err)
	}
	if _, err := store.Cancel(approval.ID, "sess-1", "dev-1"); err != nil {
		t.Fatalf("first cancellation failed: %v", err)
	}
	if _, err := store.Cancel(approval.ID, "sess-1", "dev-1"); !errors.Is(err, ErrProcessApprovalNotPending) {
		t.Fatalf("expected deterministic not-pending on repeated cancellation, got %v", err)
	}
}

// TestProcessApprovalStore_AuthorizationDispatch covers both accepted authorization forms and proves
// that neither works before a human decision.
func TestProcessApprovalStore_AuthorizationDispatch(t *testing.T) {
	store, _ := newTestStore(t)

	pending := mustCreate(t, store, "gradlew", []string{"build"})
	if _, err := store.ConsumeAuthorization(pending.ID, "sess-1", "dev-1", "/workspace", "gradlew", []string{"build"}); !errors.Is(err, ErrProcessApprovalNotApproved) {
		t.Fatalf("expected pending proposal to be refused, got %v", err)
	}

	approved := mustCreate(t, store, "gradlew", []string{"test"})
	if _, err := store.Decide(approved.ID, "sess-1", "dev-1", "approve", approved.Digest); err != nil {
		t.Fatalf("Decide failed: %v", err)
	}

	// Approval id form (agent task flow).
	if _, err := store.ConsumeAuthorization(approved.ID, "sess-1", "dev-1", "/workspace", "gradlew", []string{"test"}); err != nil {
		t.Fatalf("expected approval-id authorization to be consumed, got %v", err)
	}
	// Replay of the same authorization is refused.
	if _, err := store.ConsumeAuthorization(approved.ID, "sess-1", "dev-1", "/workspace", "gradlew", []string{"test"}); !errors.Is(err, ErrProcessApprovalNotPending) {
		t.Fatalf("expected replay of consumed approval to be refused, got %v", err)
	}

	// Execution token form (direct HTTP flow).
	second := mustCreate(t, store, "gradlew", []string{"assembleDebug"})
	decidedToken, err := store.Decide(second.ID, "sess-1", "dev-1", "approve", second.Digest)
	if err != nil {
		t.Fatalf("Decide failed: %v", err)
	}
	if decidedToken.Token == "" {
		t.Fatalf("expected an execution token")
	}
	if _, err := store.ConsumeAuthorization(decidedToken.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"assembleDebug"}); err != nil {
		t.Fatalf("expected execution-token authorization to be consumed, got %v", err)
	}
	if _, err := store.ConsumeAuthorization(decidedToken.Token, "sess-1", "dev-1", "/workspace", "gradlew", []string{"assembleDebug"}); !errors.Is(err, ErrProcessApprovalToken) {
		t.Fatalf("expected token replay to be refused, got %v", err)
	}
}

func TestProcessApprovalStore_StatusNeverExposesToken(t *testing.T) {
	store, _ := newTestStore(t)
	approval := mustCreate(t, store, "gradlew", []string{"build"})
	if _, err := store.Decide(approval.ID, "sess-1", "dev-1", "approve", approval.Digest); err != nil {
		t.Fatalf("Decide failed: %v", err)
	}

	status, err := store.Get(approval.ID, "sess-1", "dev-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if status.Token != "" {
		t.Fatalf("status representation must not include the execution token")
	}
	if status.State != ProcessApprovalApproved {
		t.Fatalf("expected approved state, got %q", status.State)
	}
}
