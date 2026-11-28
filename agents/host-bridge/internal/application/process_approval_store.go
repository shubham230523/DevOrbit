package application

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"devorbit.net/agents/host-bridge/internal/domain"
)

// Process approval lifecycle errors. Every one of them is a fail-closed outcome.
var (
	ErrProcessApprovalNotFound   = errors.New("process approval error: unknown approval id")
	ErrProcessApprovalNotPending = errors.New("process approval error: approval already decided")
	// ErrProcessApprovalNotApproved means execution was attempted while the proposal is still pending,
	// i.e. no human decision has been recorded yet.
	ErrProcessApprovalNotApproved = errors.New("process approval error: proposal has not been approved")
	ErrProcessApprovalExpired    = errors.New("process approval error: approval has expired")
	ErrProcessApprovalOwner      = errors.New("process approval error: approval belongs to a different session or device")
	ErrProcessApprovalDigest     = errors.New("process approval error: confirmation digest does not match the proposed command")
	ErrProcessApprovalScope      = errors.New("process approval error: execution does not match the approved command or workspace")
	ErrProcessApprovalToken      = errors.New("process approval error: execution token not found or already consumed")
)

// ProcessApprovalState is the lifecycle state of one approval request.
type ProcessApprovalState string

const (
	// ProcessApprovalPending means a proposal exists and nobody has decided yet. It carries no
	// execution capability whatsoever.
	ProcessApprovalPending ProcessApprovalState = "pending"
	// ProcessApprovalApproved means a human decision authorized exactly one execution; an execution
	// token now exists and has not been consumed.
	ProcessApprovalApproved ProcessApprovalState = "approved"
	// ProcessApprovalRejected means the decision was "reject". No token is ever issued.
	ProcessApprovalRejected ProcessApprovalState = "rejected"
	// ProcessApprovalExpired means the proposal lapsed before a decision or before consumption.
	ProcessApprovalExpired ProcessApprovalState = "expired"
	// ProcessApprovalConsumed means the single authorized execution has been used.
	ProcessApprovalConsumed ProcessApprovalState = "consumed"
	// ProcessApprovalCancelled means the requester abandoned the proposal before a decision. A
	// cancelled proposal can never be approved and can never execute.
	ProcessApprovalCancelled ProcessApprovalState = "cancelled"
)

// ProcessApproval is a request to execute a command that runs project-defined code (the Gradle
// family). It is created in the pending state and only a separate decision call can move it to
// approved and mint an execution token.
type ProcessApproval struct {
	ID          string               `json:"approval_id"`
	WorkspaceID string               `json:"workspace_id"`
	Command     string               `json:"command"`
	Args        []string             `json:"args"`
	Reason      string               `json:"reason"`
	Digest      string               `json:"command_digest"`
	State       ProcessApprovalState `json:"state"`
	CreatedAt   time.Time            `json:"created_at"`
	ExpiresAt   time.Time            `json:"expires_at"`

	// Owner binding: only the session and device that requested the approval may decide or consume it.
	OwnerSessionID string `json:"-"`
	OwnerDeviceID  string `json:"-"`

	// Token exists only after an approval decision. It is single-use and is never included in any
	// status representation returned to the client.
	Token     string    `json:"-"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}

// Clone returns a copy that shares no slice or token state with the stored record, so handlers can
// serialize a record without exposing mutable internal state.
func (p *ProcessApproval) Clone() *ProcessApproval {
	clone := *p
	if p.Args != nil {
		clone.Args = append([]string(nil), p.Args...)
	}
	return &clone
}

// CanonicalCommandDigest binds an approval to the exact execution request.
//
// The encoding is length-prefixed and domain-separated, so no two different (workspace, command,
// args) tuples can produce the same digest by concatenation ambiguity (for example ["a b"] versus
// ["a","b"]). The digest is not a secret: it exists so a decision must reference the precise
// proposal content the Host Bridge issued.
func CanonicalCommandDigest(workspaceID, command string, args []string) string {
	hasher := sha256.New()
	writeField := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hasher.Write(length[:])
		hasher.Write([]byte(value))
	}

	hasher.Write([]byte("devorbit-process-approval-v1\x00"))
	writeField(workspaceID)
	writeField(command)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(args)))
	hasher.Write(count[:])
	for _, arg := range args {
		writeField(arg)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// ProcessApprovalStore holds pending and decided process approvals.
//
// A dedicated store exists because the task-oriented ApprovalManager mints its token at proposal
// time, which is precisely the self-approval flaw this milestone fixes: request and grant must be
// different operations. Command classification still comes from the shared security.CommandPolicy,
// so there is no second authorization policy.
type ProcessApprovalStore struct {
	mu        sync.Mutex
	approvals map[string]*ProcessApproval // key: approval id
	byToken   map[string]string           // execution token -> approval id
	ttl       time.Duration
	now       func() time.Time
}

// NewProcessApprovalStore creates a store whose proposals live for ttl.
func NewProcessApprovalStore(ttl time.Duration) *ProcessApprovalStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &ProcessApprovalStore{
		approvals: make(map[string]*ProcessApproval),
		byToken:   make(map[string]string),
		ttl:       ttl,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// SetClock injects a clock for deterministic expiry tests.
func (s *ProcessApprovalStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func randomHex(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Create records a pending approval request. It returns no execution capability.
func (s *ProcessApprovalStore) Create(sessionID, deviceID, workspaceID, command string, args []string, reason string) (*ProcessApproval, error) {
	if workspaceID == "" || command == "" {
		return nil, domain.ErrCommandNotAllowed
	}
	idHex, err := randomHex(12)
	if err != nil {
		return nil, fmt.Errorf("failed to generate approval id: %w", err)
	}

	now := s.now()
	approval := &ProcessApproval{
		ID:             "pappr-" + idHex,
		WorkspaceID:    workspaceID,
		Command:        command,
		Args:           append([]string(nil), args...),
		Reason:         reason,
		Digest:         CanonicalCommandDigest(workspaceID, command, args),
		State:          ProcessApprovalPending,
		CreatedAt:      now,
		ExpiresAt:      now.Add(s.ttl),
		OwnerSessionID: sessionID,
		OwnerDeviceID:  deviceID,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(now)
	s.approvals[approval.ID] = approval
	return approval.Clone(), nil
}

// Get returns the approval state to its owner. The execution token is never part of the result: a
// status read must not hand out the capability itself.
func (s *ProcessApprovalStore) Get(id, sessionID, deviceID string) (*ProcessApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[id]
	if !ok {
		return nil, ErrProcessApprovalNotFound
	}
	if approval.OwnerSessionID != sessionID || approval.OwnerDeviceID != deviceID {
		return nil, ErrProcessApprovalOwner
	}
	s.expireLocked(approval, s.now())

	status := approval.Clone()
	status.Token = ""
	return status, nil
}

// Decide records the human decision for a pending approval.
//
// Approving requires the caller to echo the digest of the proposal it was shown, so a decision can
// only apply to the exact command the Host Bridge issued. Rejecting needs no digest and never mints
// a token.
func (s *ProcessApprovalStore) Decide(id, sessionID, deviceID, decision, digest string) (*ProcessApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[id]
	if !ok {
		return nil, ErrProcessApprovalNotFound
	}
	if approval.OwnerSessionID != sessionID || approval.OwnerDeviceID != deviceID {
		return nil, ErrProcessApprovalOwner
	}
	if s.expireLocked(approval, s.now()) {
		return nil, ErrProcessApprovalExpired
	}
	if approval.State != ProcessApprovalPending {
		return nil, ErrProcessApprovalNotPending
	}

	switch decision {
	case "reject":
		approval.State = ProcessApprovalRejected
		approval.DecidedAt = s.now()
		return approval.Clone(), nil
	case "approve":
		if subtle.ConstantTimeCompare([]byte(digest), []byte(approval.Digest)) != 1 {
			return nil, ErrProcessApprovalDigest
		}
		token, err := randomHex(24)
		if err != nil {
			return nil, fmt.Errorf("failed to generate execution token: %w", err)
		}
		approval.State = ProcessApprovalApproved
		approval.DecidedAt = s.now()
		approval.Token = "pexec-" + token
		s.byToken[approval.Token] = approval.ID
		return approval.Clone(), nil
	default:
		return nil, ErrProcessApprovalDigest
	}
}

// Cancel abandons a pending proposal on the server side, so the client no longer has to rely on the
// proposal silently expiring. Only a pending proposal may be cancelled; a decision that already
// happened is a terminal state and is reported as such.
//
// Repeated cancellation is deterministic: the second call reports the proposal as not pending rather
// than changing anything. A cancel racing with a decision is resolved by the store mutex, so exactly
// one terminal outcome wins.
func (s *ProcessApprovalStore) Cancel(id, sessionID, deviceID string) (*ProcessApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[id]
	if !ok {
		return nil, ErrProcessApprovalNotFound
	}
	if approval.OwnerSessionID != sessionID || approval.OwnerDeviceID != deviceID {
		return nil, ErrProcessApprovalOwner
	}
	if s.expireLocked(approval, s.now()) {
		return nil, ErrProcessApprovalExpired
	}
	if approval.State != ProcessApprovalPending {
		return nil, ErrProcessApprovalNotPending
	}

	approval.State = ProcessApprovalCancelled
	approval.DecidedAt = s.now()
	return approval.Clone(), nil
}

// Consume validates and uses the single authorized execution. It revalidates the full binding
// immediately before process launch and deletes the token so a replay cannot succeed.
func (s *ProcessApprovalStore) Consume(token, sessionID, deviceID, workspaceID, command string, args []string) (*ProcessApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approvalID, ok := s.byToken[token]
	if !ok {
		return nil, ErrProcessApprovalToken
	}
	approval, exists := s.approvals[approvalID]
	if !exists {
		delete(s.byToken, token)
		return nil, ErrProcessApprovalToken
	}

	if approval.OwnerSessionID != sessionID || approval.OwnerDeviceID != deviceID {
		return nil, ErrProcessApprovalOwner
	}
	if s.expireLocked(approval, s.now()) {
		delete(s.byToken, token)
		return nil, ErrProcessApprovalExpired
	}
	if approval.State != ProcessApprovalApproved {
		return nil, ErrProcessApprovalToken
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(approval.Token)) != 1 {
		return nil, ErrProcessApprovalToken
	}

	// Revalidate the execution request against the approved content. The command that runs is the
	// approved command, byte for byte.
	expected := CanonicalCommandDigest(approval.WorkspaceID, approval.Command, approval.Args)
	if approval.WorkspaceID != workspaceID || CanonicalCommandDigest(workspaceID, command, args) != expected {
		return nil, ErrProcessApprovalScope
	}

	approval.State = ProcessApprovalConsumed
	approval.Token = ""
	delete(s.byToken, token)
	return approval.Clone(), nil
}

// ConsumeAuthorization consumes an authorization presented by any route.
//
// Two identifiers can legitimately appear here and both mean the same thing — a recorded human
// decision for this exact execution:
//   - an execution token (`pexec-…`) issued to the deciding client;
//   - an approval id (`pappr-…`) carried by the agent task flow, where the human decision was recorded
//     against the proposal and the token never leaves the host.
//
// Dispatch lives here so that every route passes through one consumption point.
func (s *ProcessApprovalStore) ConsumeAuthorization(authorization, sessionID, deviceID, workspaceID, command string, args []string) (*ProcessApproval, error) {
	if strings.HasPrefix(authorization, "pappr-") {
		return s.ConsumeByApprovalID(authorization, sessionID, deviceID, workspaceID, command, args)
	}
	return s.Consume(authorization, sessionID, deviceID, workspaceID, command, args)
}

// ConsumeByApprovalID consumes an approval that a human has already approved, addressed by its id.
//
// The agent task flow carries the approval id (the value the AI was given when the proposal was
// created) rather than the execution token, so the token itself never travels through the agent.
// Consumption still requires the store state to be approved — which only a human decision can
// produce — and still revalidates the full execution scope.
func (s *ProcessApprovalStore) ConsumeByApprovalID(id, sessionID, deviceID, workspaceID, command string, args []string) (*ProcessApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[id]
	if !ok {
		return nil, ErrProcessApprovalNotFound
	}
	if approval.OwnerSessionID != sessionID || approval.OwnerDeviceID != deviceID {
		return nil, ErrProcessApprovalOwner
	}
	if s.expireLocked(approval, s.now()) {
		return nil, ErrProcessApprovalExpired
	}
	if approval.State == ProcessApprovalPending {
		return nil, ErrProcessApprovalNotApproved
	}
	if approval.State != ProcessApprovalApproved || approval.Token == "" {
		return nil, ErrProcessApprovalNotPending
	}

	expected := CanonicalCommandDigest(approval.WorkspaceID, approval.Command, approval.Args)
	if approval.WorkspaceID != workspaceID || CanonicalCommandDigest(workspaceID, command, args) != expected {
		return nil, ErrProcessApprovalScope
	}

	delete(s.byToken, approval.Token)
	approval.Token = ""
	approval.State = ProcessApprovalConsumed
	return approval.Clone(), nil
}

// expireLocked marks a lapsed approval expired. It reports whether the approval is (now) unusable.
func (s *ProcessApprovalStore) expireLocked(approval *ProcessApproval, now time.Time) bool {
	// Only time-based expiry is reported here. Terminal states (consumed, rejected, cancelled) are
	// detected by each caller so the resulting error stays precise instead of being reported as
	// "expired".
	if now.After(approval.ExpiresAt) {
		if approval.Token != "" {
			delete(s.byToken, approval.Token)
			approval.Token = ""
		}
		if approval.State == ProcessApprovalPending || approval.State == ProcessApprovalApproved {
			approval.State = ProcessApprovalExpired
		}
		return true
	}
	return false
}

// gcLocked drops decided or expired approvals that are no longer actionable, bounding memory.
func (s *ProcessApprovalStore) gcLocked(now time.Time) {
	if len(s.approvals) < 256 {
		return
	}
	for id, approval := range s.approvals {
		if approval.State == ProcessApprovalConsumed || approval.State == ProcessApprovalRejected ||
			approval.State == ProcessApprovalExpired || now.After(approval.ExpiresAt) {
			if approval.Token != "" {
				delete(s.byToken, approval.Token)
			}
			delete(s.approvals, id)
		}
	}
}
