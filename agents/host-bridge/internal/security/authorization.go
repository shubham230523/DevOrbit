package security

import (
	"fmt"
	"path/filepath"
	"strings"

	"devorbit.net/agents/host-bridge/internal/domain"
)

type ActionPermission string

const (
	PermissionWorkspaceList     ActionPermission = "workspace:read"
	PermissionWorkspaceDiscover ActionPermission = "workspace:discover"
	PermissionFileRead          ActionPermission = "file:read"
	PermissionFileWrite         ActionPermission = "file:write"
	PermissionGitInspect        ActionPermission = "git:read"
	PermissionProcessRead       ActionPermission = "process:read"
	PermissionProcessExecute    ActionPermission = "process:execute"
	PermissionDeviceInspect     ActionPermission = "device:read"
	PermissionAIExecute         ActionPermission = "ai:prompt"
	PermissionAgentRead         ActionPermission = "agent:read"
	PermissionAgentExecute      ActionPermission = "agent:execute"
	PermissionAgentApprove      ActionPermission = "agent:approve"
	PermissionGitHubRead        ActionPermission = "github:read"
	PermissionGitHubClone       ActionPermission = "github:clone"
)

type SessionPermissions struct {
	Granted map[ActionPermission]bool
}

func NewFullSessionPermissions() *SessionPermissions {
	return &SessionPermissions{
		Granted: map[ActionPermission]bool{
			PermissionWorkspaceList:     true,
			PermissionWorkspaceDiscover: true,
			PermissionFileRead:          true,
			PermissionFileWrite:         true,
			PermissionGitInspect:        true,
			PermissionProcessRead:       true,
			PermissionProcessExecute:    true,
			PermissionDeviceInspect:     true,
			PermissionAIExecute:         true,
			PermissionAgentRead:         true,
			PermissionAgentExecute:      true,
			PermissionAgentApprove:      true,
			PermissionGitHubRead:        true,
			PermissionGitHubClone:       true,
		},
	}
}

// NewScopedSessionPermissions constructs a SessionPermissions granted only the specified permissions.
func NewScopedSessionPermissions(perms ...ActionPermission) *SessionPermissions {
	granted := make(map[ActionPermission]bool, len(perms))
	for _, p := range perms {
		granted[p] = true
	}
	return &SessionPermissions{Granted: granted}
}

func (sp *SessionPermissions) IsAuthorized(action ActionPermission) bool {
	if sp == nil || sp.Granted == nil {
		return false
	}
	return sp.Granted[action]
}

type CommandPolicy struct {
	AllowedBinaries map[string]bool
}

func NewDefaultCommandPolicy() *CommandPolicy {
	return &CommandPolicy{
		AllowedBinaries: map[string]bool{
			"git":         true,
			"git.exe":     true,
			"gradlew":     true,
			"gradlew.bat": true,
			"gradle":      true,
			"gradle.bat":  true,
			"adb":         true,
			"adb.exe":     true,
		},
	}
}

func (p *CommandPolicy) ValidateCommand(command string, args []string) error {
	baseName := strings.ToLower(filepath.Base(command))
	if !p.AllowedBinaries[baseName] && !p.AllowedBinaries[command] {
		return fmt.Errorf("%w: binary '%s' is not authorized", domain.ErrCommandNotAllowed, command)
	}

	for _, arg := range args {
		if strings.ContainsAny(arg, ";&|`$<>") {
			return fmt.Errorf("%w: argument '%s' contains forbidden shell characters", domain.ErrCommandNotAllowed, arg)
		}
	}

	return nil
}
