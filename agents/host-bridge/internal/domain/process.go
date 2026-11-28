package domain

import "errors"

var ErrCommandNotAllowed = errors.New("command execution rejected: command not in authorization allowlist")

type ProcessSpec struct {
	WorkspaceID    string   `json:"workspace_id"`
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	// ApprovalToken carries a single-use execution token issued by a human approval decision. It is
	// consumed by the process approval gate before the process is started and is never passed to the
	// child process.
	ApprovalToken string `json:"approval_token,omitempty"`
	// OwnerSessionID and OwnerDeviceID are derived from the authenticated request (or the owning agent
	// task). They are never accepted from client JSON and are required to bind an approval to the
	// principal that requested it.
	OwnerSessionID string `json:"-"`
	OwnerDeviceID  string `json:"-"`
}

type ProcessStatus string

const (
	ProcessStatusRunning   ProcessStatus = "running"
	ProcessStatusCompleted ProcessStatus = "completed"
	ProcessStatusFailed    ProcessStatus = "failed"
	ProcessStatusCancelled ProcessStatus = "cancelled"
)

type ProcessInfo struct {
	ID        string        `json:"process_id"`
	Command   string        `json:"command"`
	Args      []string      `json:"args"`
	Status    ProcessStatus `json:"status"`
	ExitCode  int           `json:"exit_code"`
	Stdout    string        `json:"stdout"`
	Stderr    string        `json:"stderr"`
	StartTime string        `json:"start_time"`
	EndTime   string        `json:"end_time,omitempty"`
}
