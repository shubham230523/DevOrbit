package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"devorbit.net/agents/ai-router/pkg/router"
	"devorbit.net/agents/host-bridge/internal/domain"
)

var (
	ErrApprovalRequired = errors.New("approval required for tool execution")
)

type AgentToolExecutor struct {
	workspaceSvc  *WorkspaceService
	filesystemSvc *FilesystemService
	processSvc    *ProcessService
	gitSvc        *GitService
	deviceSvc     *DeviceService
	approvalMgr   *ApprovalManager
	// processApprovals is the authoritative approval service for commands that execute
	// project-defined code. The legacy approvalMgr is NOT an authorization route for those commands.
	processApprovals *ProcessApprovalStore
}

// SetProcessApprovalStore installs the authoritative process approval service used for Gradle-family
// commands. Without it those commands fail closed.
func (e *AgentToolExecutor) SetProcessApprovalStore(store *ProcessApprovalStore) {
	e.processApprovals = store
}

// taskOwnerContextKey carries the owning session and device of the running task so a process
// approval can be bound to the same principal that would authorize it over HTTP.
type taskOwnerContextKey struct{}

type taskOwner struct {
	SessionID string
	DeviceID  string
}

// WithTaskOwner attaches the owning session and device to a task execution context.
func WithTaskOwner(ctx context.Context, sessionID, deviceID string) context.Context {
	return context.WithValue(ctx, taskOwnerContextKey{}, taskOwner{SessionID: sessionID, DeviceID: deviceID})
}

// taskOwnerFromContext returns the owning principal recorded on the context.
func taskOwnerFromContext(ctx context.Context) (string, string) {
	if owner, ok := ctx.Value(taskOwnerContextKey{}).(taskOwner); ok {
		return owner.SessionID, owner.DeviceID
	}
	return "", ""
}

func NewAgentToolExecutor(
	ws *WorkspaceService,
	fs *FilesystemService,
	ps *ProcessService,
	gs *GitService,
	ds *DeviceService,
	approvalMgr *ApprovalManager,
) *AgentToolExecutor {
	return &AgentToolExecutor{
		workspaceSvc:  ws,
		filesystemSvc: fs,
		processSvc:    ps,
		gitSvc:        gs,
		deviceSvc:     ds,
		approvalMgr:   approvalMgr,
	}
}

func (e *AgentToolExecutor) ExecuteTool(
	ctx context.Context,
	taskID string,
	workspaceID string,
	call *router.FunctionCall,
	approvalToken string,
) (map[string]any, *ApprovalProposal, error) {
	if call == nil {
		return nil, nil, errors.New("nil function call")
	}

	toolName := call.Name
	args := call.Arguments
	if args == nil {
		args = make(map[string]any)
	}

	switch toolName {
	case router.ToolListProjects:
		workspaces, err := e.workspaceSvc.ListWorkspaces()
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"projects": workspaces}, nil, nil

	case router.ToolListFiles:
		relPath, _ := args["path"].(string)
		listing, err := e.filesystemSvc.ListDirectory(workspaceID, relPath)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"workspace_id": workspaceID,
			"path":         listing.RelativePath,
			"entries":      listing.Entries,
		}, nil, nil

	case router.ToolReadFile:
		relPath, _ := args["path"].(string)
		res, err := e.filesystemSvc.ReadFile(workspaceID, relPath)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"workspace_id": workspaceID,
			"path":         res.RelativePath,
			"content":      res.Content,
			"sha256":       res.SHA256Hash,
			"size_bytes":   res.SizeBytes,
		}, nil, nil

	case router.ToolSearchFiles:
		pattern, _ := args["pattern"].(string)
		subPath, _ := args["path"].(string)
		caseSensitive, _ := args["case_sensitive"].(bool)

		res, err := e.filesystemSvc.SearchFiles(workspaceID, subPath, pattern, caseSensitive)
		if err != nil {
			return map[string]any{
				"workspace_id": workspaceID,
				"pattern":      pattern,
				"matches":      []any{},
			}, nil, nil
		}
		return map[string]any{
			"workspace_id": workspaceID,
			"pattern":      pattern,
			"matches":      res,
		}, nil, nil

	case router.ToolCreateFile:
		relPath, _ := args["path"].(string)
		content, _ := args["content"].(string)

		if approvalToken == "" {
			proposal, err := e.approvalMgr.CreateProposal(
				taskID, workspaceID, toolName, relPath, "", content, "", "", nil,
				fmt.Sprintf("Create new file '%s'", relPath), 10*time.Minute,
			)
			if err != nil {
				return nil, nil, err
			}
			return nil, proposal, ErrApprovalRequired
		}

		_, err := e.approvalMgr.ConsumeApproval(approvalToken, taskID, workspaceID, toolName, relPath, "", "", nil, content, "")
		if err != nil {
			return nil, nil, err
		}

		writeReq := &domain.WriteFileRequest{
			WorkspaceID:  workspaceID,
			RelativePath: relPath,
			NewContent:   content,
			BaseSHA256:   "",
		}
		res, err := e.filesystemSvc.WriteFile(writeReq)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"status":     "created",
			"path":       relPath,
			"new_sha256": res.UpdatedSHA256,
		}, nil, nil

	case router.ToolEditFile:
		relPath, _ := args["path"].(string)
		target, _ := args["target"].(string)
		replacement, _ := args["replacement"].(string)

		existing, err := e.filesystemSvc.ReadFile(workspaceID, relPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read target file before editing: %w", err)
		}

		if approvalToken == "" {
			proposal, err := e.approvalMgr.CreateProposal(
				taskID, workspaceID, toolName, relPath, existing.SHA256Hash, target, replacement, "", nil,
				fmt.Sprintf("Edit file '%s' (replace '%s')", relPath, target), 10*time.Minute,
			)
			if err != nil {
				return nil, nil, err
			}
			return nil, proposal, ErrApprovalRequired
		}

		proposal, err := e.approvalMgr.ConsumeApproval(approvalToken, taskID, workspaceID, toolName, relPath, existing.SHA256Hash, "", nil, target, replacement)
		if err != nil {
			return nil, nil, err
		}

		if existing.SHA256Hash != proposal.ExpectedSHA256 {
			return nil, nil, errors.New("conflict: file modified since approval proposal was generated")
		}

		newContent, err := applySearchAndReplace(existing.Content, target, replacement)
		if err != nil {
			return nil, nil, err
		}

		writeReq := &domain.WriteFileRequest{
			WorkspaceID:  workspaceID,
			RelativePath: relPath,
			NewContent:   newContent,
			BaseSHA256:   existing.SHA256Hash,
		}
		res, err := e.filesystemSvc.WriteFile(writeReq)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"status":     "edited",
			"path":       relPath,
			"old_sha256": existing.SHA256Hash,
			"new_sha256": res.UpdatedSHA256,
		}, nil, nil

	case router.ToolDeleteFile:
		relPath, _ := args["path"].(string)

		if approvalToken == "" {
			proposal, err := e.approvalMgr.CreateProposal(
				taskID, workspaceID, toolName, relPath, "", "", "", "", nil,
				fmt.Sprintf("Delete file '%s'", relPath), 10*time.Minute,
			)
			if err != nil {
				return nil, nil, err
			}
			return nil, proposal, ErrApprovalRequired
		}

		_, err := e.approvalMgr.ConsumeApproval(approvalToken, taskID, workspaceID, toolName, relPath, "", "", nil, "", "")
		if err != nil {
			return nil, nil, err
		}

		err = e.filesystemSvc.DeleteFile(workspaceID, relPath)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"status": "deleted",
			"path":   relPath,
		}, nil, nil

	case router.ToolExecuteCommand:
		cmdStr, _ := args["command"].(string)
		rawArgs, _ := args["args"].([]any)
		cmdArgs := make([]string, 0, len(rawArgs))
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				cmdArgs = append(cmdArgs, s)
			}
		}

		ownerSession, ownerDevice := taskOwnerFromContext(ctx)
		spec := &domain.ProcessSpec{
			WorkspaceID:    workspaceID,
			Command:        cmdStr,
			Args:           cmdArgs,
			TimeoutSeconds: 30,
			OwnerSessionID: ownerSession,
			OwnerDeviceID:  ownerDevice,
		}

		// Commands that execute project-defined code are authorized by the process approval service
		// only. The legacy task ApprovalManager is deliberately not consulted for them, so an old
		// task-approval token can never authorize a Gradle execution.
		if requiresApproval, policyReason := e.processSvc.RequiresApproval(spec); requiresApproval {
			if e.processApprovals == nil {
				return nil, nil, ErrApprovalRequirementMissing
			}
			if ownerSession == "" || ownerDevice == "" {
				return nil, nil, ErrProcessApprovalOwner
			}

			if approvalToken == "" {
				approval, err := e.processApprovals.Create(
					ownerSession, ownerDevice, workspaceID, cmdStr, cmdArgs, policyReason,
				)
				if err != nil {
					return nil, nil, err
				}
				// The engine receives a proposal-shaped view so the task pauses and the client can
				// display the exact command. Token is the approval id, which grants nothing on its own;
				// ExpectedSHA256 carries the digest the human decision must echo.
				return nil, &ApprovalProposal{
					Token:          approval.ID,
					TaskID:         taskID,
					WorkspaceID:    workspaceID,
					ToolName:       toolName,
					Command:        cmdStr,
					Args:           append([]string(nil), cmdArgs...),
					ExpectedSHA256: approval.Digest,
					Reason:         fmt.Sprintf("Process approval required: %s", policyReason),
					CreatedAt:      approval.CreatedAt,
					ExpiresAt:      approval.ExpiresAt,
				}, ErrApprovalRequired
			}

			// The human decision was recorded against this proposal. The authorization is consumed by
			// the shared ProcessService gate, so the tool never authorizes execution itself.
			spec.ApprovalToken = approvalToken

			info, err := e.processSvc.ExecuteProcess(ctx, spec)
			if err != nil {
				return nil, nil, err
			}
			return map[string]any{
				"process_id": info.ID,
				"command":    info.Command,
				"exit_code":  info.ExitCode,
				"stdout":     info.Stdout,
				"stderr":     info.Stderr,
			}, nil, nil
		}

		// Non-Gradle commands keep the existing task-level approval behaviour.
		if approvalToken == "" {
			proposal, err := e.approvalMgr.CreateProposal(
				taskID, workspaceID, toolName, "", "", "", "", cmdStr, cmdArgs,
				fmt.Sprintf("Execute command '%s %v'", cmdStr, cmdArgs), 10*time.Minute,
			)
			if err != nil {
				return nil, nil, err
			}
			return nil, proposal, ErrApprovalRequired
		}

		_, err := e.approvalMgr.ConsumeApproval(approvalToken, taskID, workspaceID, toolName, "", "", cmdStr, cmdArgs, "", "")
		if err != nil {
			return nil, nil, err
		}

		info, err := e.processSvc.ExecuteProcess(ctx, spec)
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{
			"process_id": info.ID,
			"command":    info.Command,
			"exit_code":  info.ExitCode,
			"stdout":     info.Stdout,
			"stderr":     info.Stderr,
		}, nil, nil

	case router.ToolGetCommandOutput:
		procID, _ := args["task_id"].(string)
		info, ok := e.processSvc.GetProcessStatus(procID)
		if !ok {
			return nil, nil, fmt.Errorf("process '%s' not found", procID)
		}
		return map[string]any{
			"process_id": info.ID,
			"status":     info.Status,
			"exit_code":  info.ExitCode,
			"stdout":     info.Stdout,
			"stderr":     info.Stderr,
		}, nil, nil

	case router.ToolCancelTask:
		procID, _ := args["task_id"].(string)
		ok := e.processSvc.CancelProcess(procID)
		return map[string]any{
			"process_id": procID,
			"cancelled":  ok,
		}, nil, nil

	default:
		return nil, nil, fmt.Errorf("unknown or unsupported tool '%s'", toolName)
	}
}

func applySearchAndReplace(content, target, replacement string) (string, error) {
	if target == "" {
		return "", errors.New("edit_file target string cannot be empty")
	}

	idx := filepath.Clean(target)
	_ = idx

	if !containsExactSubstring(content, target) {
		return "", fmt.Errorf("target snippet '%s' not found in target file content", target)
	}

	return replaceFirst(content, target, replacement), nil
}

func containsExactSubstring(s, substr string) bool {
	return len(substr) > 0 && (s == substr || len(s) >= len(substr) && findSubstr(s, substr) >= 0)
}

func findSubstr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func replaceFirst(s, target, replacement string) string {
	idx := findSubstr(s, target)
	if idx < 0 {
		return s
	}
	return s[:idx] + replacement + s[idx+len(target):]
}
