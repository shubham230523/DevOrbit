package security

// Command authorization lives in command_policy.go: a single argument-aware policy used by every
// command execution boundary (HTTP process execution and the agent execute_command tool).

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
