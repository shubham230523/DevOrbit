package domain

import "errors"

var ErrCommandNotAllowed = errors.New("command execution rejected: command not in authorization allowlist")

type ProcessSpec struct {
	WorkspaceID    string   `json:"workspace_id"`
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
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
