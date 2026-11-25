package application

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrApprovalTokenNotFound = errors.New("approval error: token not found or already consumed")
	ErrApprovalExpired       = errors.New("approval error: approval token has expired")
	ErrApprovalMismatch      = errors.New("approval error: operation parameters do not match approved token")
)

type ApprovalProposal struct {
	Token          string    `json:"approval_token"`
	TaskID         string    `json:"task_id"`
	WorkspaceID    string    `json:"workspace_id"`
	ToolName       string    `json:"tool_name"`
	TargetPath     string    `json:"target_path,omitempty"`
	ExpectedSHA256 string    `json:"expected_sha256,omitempty"`
	TargetContent  string    `json:"target_content,omitempty"`
	Replacement    string    `json:"replacement,omitempty"`
	Command        string    `json:"command,omitempty"`
	Args           []string  `json:"args,omitempty"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	Consumed       bool      `json:"consumed"`
}

type ApprovalManager struct {
	mu        sync.RWMutex
	proposals map[string]*ApprovalProposal // Key: Token
}

func NewApprovalManager() *ApprovalManager {
	return &ApprovalManager{
		proposals: make(map[string]*ApprovalProposal),
	}
}

func (m *ApprovalManager) CreateProposal(
	taskID, workspaceID, toolName, targetPath, expectedSHA256, targetContent, replacement, command string,
	args []string, reason string, ttl time.Duration,
) (*ApprovalProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate approval token: %w", err)
	}
	token := fmt.Sprintf("appr-%s", hex.EncodeToString(tokenBytes))

	now := time.Now().UTC()
	proposal := &ApprovalProposal{
		Token:          token,
		TaskID:         taskID,
		WorkspaceID:    workspaceID,
		ToolName:       toolName,
		TargetPath:     targetPath,
		ExpectedSHA256: expectedSHA256,
		TargetContent:  targetContent,
		Replacement:    replacement,
		Command:        command,
		Args:           args,
		Reason:         reason,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
		Consumed:       false,
	}

	m.proposals[token] = proposal
	return proposal, nil
}

func (m *ApprovalManager) ConsumeApproval(
	token, taskID, workspaceID, toolName, targetPath, expectedSHA256, command string, args []string, targetContent, replacement string,
) (*ApprovalProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, found := m.proposals[token]
	if !found || p.Consumed {
		return nil, ErrApprovalTokenNotFound
	}

	if time.Now().UTC().After(p.ExpiresAt) {
		delete(m.proposals, token)
		return nil, ErrApprovalExpired
	}

	// Validate exact binding for ALL fields unconditionally
	if p.TaskID != taskID || p.WorkspaceID != workspaceID || p.ToolName != toolName {
		return nil, ErrApprovalMismatch
	}

	if p.TargetPath != targetPath {
		return nil, ErrApprovalMismatch
	}

	if p.ExpectedSHA256 != expectedSHA256 {
		return nil, ErrApprovalMismatch
	}

	if p.Command != command {
		return nil, ErrApprovalMismatch
	}

	if !equalStringSlices(p.Args, args) {
		return nil, ErrApprovalMismatch
	}

	if p.TargetContent != targetContent {
		return nil, ErrApprovalMismatch
	}

	if p.Replacement != replacement {
		return nil, ErrApprovalMismatch
	}

	// Constant-time token verification
	if subtle.ConstantTimeCompare([]byte(token), []byte(p.Token)) != 1 {
		return nil, ErrApprovalMismatch
	}

	p.Consumed = true
	return p, nil
}

func equalStringSlices(a, b []string) bool {
	if a == nil && b == nil {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
