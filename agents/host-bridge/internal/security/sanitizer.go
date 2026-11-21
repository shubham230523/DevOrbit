package security

import (
	"fmt"
	"path/filepath"
	"strings"

	"devorbit.net/agents/host-bridge/internal/domain"
)

type WorkspaceJail struct {
	allowedRoots map[string]string // Key: workspace ID / name, Value: cleaned absolute root path
}

func NewWorkspaceJail(roots []string) *WorkspaceJail {
	m := make(map[string]string)
	for i, root := range roots {
		if root == "" {
			continue
		}
		clean := filepath.Clean(root)
		eval, err := filepath.EvalSymlinks(clean)
		if err != nil {
			eval = clean
		}
		id := fmt.Sprintf("ws-%d", i+1)
		m[id] = eval
		m[clean] = eval
	}
	return &WorkspaceJail{allowedRoots: m}
}

func (j *WorkspaceJail) AddRoot(id, rootPath string) string {
	clean := filepath.Clean(rootPath)
	eval, err := filepath.EvalSymlinks(clean)
	if err != nil {
		eval = clean
	}
	j.allowedRoots[id] = eval
	j.allowedRoots[eval] = eval
	return eval
}

func (j *WorkspaceJail) GetRoot(workspaceIDorPath string) (string, bool) {
	root, found := j.allowedRoots[workspaceIDorPath]
	return root, found
}

func (j *WorkspaceJail) ListAllowedRoots() map[string]string {
	result := make(map[string]string)
	for k, v := range j.allowedRoots {
		result[k] = v
	}
	return result
}

func (j *WorkspaceJail) ResolveAndValidate(workspaceIDorPath, relativeOrAbsPath string) (string, error) {
	rootPath, found := j.allowedRoots[workspaceIDorPath]
	if !found {
		// Try resolving if passed as a root path directly
		cleanCandidate := filepath.Clean(workspaceIDorPath)
		evalCandidate, err := filepath.EvalSymlinks(cleanCandidate)
		if err == nil {
			if r, ok := j.allowedRoots[evalCandidate]; ok {
				rootPath = r
				found = true
			}
		}
	}

	if !found {
		return "", fmt.Errorf("%w: workspace '%s' not found in allowlist", domain.ErrAccessDenied, workspaceIDorPath)
	}

	var targetPath string
	if filepath.IsAbs(relativeOrAbsPath) {
		targetPath = relativeOrAbsPath
	} else {
		targetPath = filepath.Join(rootPath, relativeOrAbsPath)
	}

	cleanTarget := filepath.Clean(targetPath)
	evalTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		evalTarget = cleanTarget
	}

	if !j.isSubpath(evalTarget, rootPath) {
		return "", fmt.Errorf("%w: target path '%s' escapes workspace root '%s'", domain.ErrAccessDenied, relativeOrAbsPath, rootPath)
	}

	return evalTarget, nil
}

func (j *WorkspaceJail) isSubpath(target, root string) bool {
	targetFold := strings.ToLower(filepath.Clean(target))
	rootFold := strings.ToLower(filepath.Clean(root))

	if targetFold == rootFold {
		return true
	}

	prefix := rootFold
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}

	return strings.HasPrefix(targetFold, prefix)
}
