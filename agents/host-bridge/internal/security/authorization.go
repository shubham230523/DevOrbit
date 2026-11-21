package security

import (
	"fmt"
	"path/filepath"
	"strings"

	"devorbit.net/agents/host-bridge/internal/domain"
)

type ActionPermission string

const (
	PermissionWorkspaceList  ActionPermission = "workspace:read"
	PermissionFileRead       ActionPermission = "file:read"
	PermissionFileWrite      ActionPermission = "file:write"
	PermissionGitInspect     ActionPermission = "git:read"
	PermissionProcessExecute ActionPermission = "process:execute"
	PermissionDeviceInspect  ActionPermission = "device:read"
)

type SessionPermissions struct {
	Granted map[ActionPermission]bool
}

func NewFullSessionPermissions() *SessionPermissions {
	return &SessionPermissions{
		Granted: map[ActionPermission]bool{
			PermissionWorkspaceList:  true,
			PermissionFileRead:       true,
			PermissionFileWrite:      true,
			PermissionGitInspect:     true,
			PermissionProcessExecute: true,
			PermissionDeviceInspect:  true,
		},
	}
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
