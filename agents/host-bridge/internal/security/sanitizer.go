package security

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"devorbit.net/agents/host-bridge/internal/domain"
)

type WorkspaceJail struct {
	mu           sync.RWMutex
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
	j.mu.Lock()
	defer j.mu.Unlock()
	j.allowedRoots[id] = eval
	j.allowedRoots[eval] = eval
	return eval
}

func (j *WorkspaceJail) GetRoot(workspaceIDorPath string) (string, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	root, found := j.allowedRoots[workspaceIDorPath]
	return root, found
}

func (j *WorkspaceJail) ListAllowedRoots() map[string]string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	result := make(map[string]string)
	for k, v := range j.allowedRoots {
		result[k] = v
	}
	return result
}

func (j *WorkspaceJail) ResolveAndValidate(workspaceIDorPath, relativeOrAbsPath string) (string, error) {
	j.mu.RLock()
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
	j.mu.RUnlock()

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
	evalTarget, err := evalSymlinksDeepest(cleanTarget)
	if err != nil {
		evalTarget = cleanTarget
	}

	if !j.isSubpath(evalTarget, rootPath) {
		return "", fmt.Errorf("%w: target path '%s' escapes workspace root '%s'", domain.ErrAccessDenied, relativeOrAbsPath, rootPath)
	}

	return evalTarget, nil
}

func evalSymlinksDeepest(path string) (string, error) {
	clean := filepath.Clean(path)
	eval, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return eval, nil
	}

	dir := filepath.Dir(clean)
	base := filepath.Base(clean)

	if dir == clean || dir == "." {
		return clean, nil
	}

	evalDir, err := evalSymlinksDeepest(dir)
	if err != nil {
		return clean, nil
	}

	return filepath.Join(evalDir, base), nil
}

func (j *WorkspaceJail) isSubpath(target, root string) bool {
	targetClean := filepath.Clean(target)
	rootClean := filepath.Clean(root)

	var targetCmp, rootCmp string
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		targetCmp = strings.ToLower(targetClean)
		rootCmp = strings.ToLower(rootClean)
	} else {
		targetCmp = targetClean
		rootCmp = rootClean
	}

	if targetCmp == rootCmp {
		return true
	}

	prefix := rootCmp
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}

	return strings.HasPrefix(targetCmp, prefix)
}
