package sanitizer

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type PathSanitizer struct {
	AllowedRoots []string
}

func NewPathSanitizer(roots []string) *PathSanitizer {
	cleanedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		clean := filepath.Clean(root)
		eval, err := filepath.EvalSymlinks(clean)
		if err == nil {
			cleanedRoots = append(cleanedRoots, eval)
		} else {
			cleanedRoots = append(cleanedRoots, clean)
		}
	}
	return &PathSanitizer{AllowedRoots: cleanedRoots}
}

func (s *PathSanitizer) ResolveAndValidate(requestedPath string) (string, error) {
	if requestedPath == "" {
		return "", errors.New("empty path requested")
	}

	cleanPath := filepath.Clean(requestedPath)
	evalPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		evalPath = cleanPath
	}

	for _, root := range s.AllowedRoots {
		if s.isSubpath(evalPath, root) {
			return evalPath, nil
		}
	}

	return "", fmt.Errorf("access denied: path '%s' is outside allowed workspace boundaries", requestedPath)
}

func (s *PathSanitizer) isSubpath(target, root string) bool {
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
