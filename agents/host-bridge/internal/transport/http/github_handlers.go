package http

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"devorbit.net/agents/host-bridge/internal/security"
)

type githubAPIRepo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	Description   string `json:"description"`
	Language      string `json:"language"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	UpdatedAt     string `json:"updated_at"`
	DefaultBranch string `json:"default_branch"`
}

func (s *Server) resolveGitHubToken(r *http.Request) string {
	if r != nil {
		if tok := strings.TrimSpace(r.Header.Get("X-GitHub-Token")); tok != "" {
			return tok
		}
	}
	return strings.TrimSpace(os.Getenv("DEVORBIT_GITHUB_TOKEN"))
}

func (s *Server) getGitHubAPIBaseURL() string {
	if s.githubAPIBaseURL != "" {
		return strings.TrimRight(s.githubAPIBaseURL, "/")
	}
	return "https://api.github.com"
}

func (s *Server) getGitHubCloneBaseDir() string {
	if s.githubCloneBaseDir != "" {
		return s.githubCloneBaseDir
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return filepath.Join(os.TempDir(), "DevOrbitProjects")
	}
	return filepath.Join(homeDir, "DevOrbitProjects")
}

func isValidRepoName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 120 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func (s *Server) HandleListGitHubRepos(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionGitHubRead); !ok {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := s.resolveGitHubToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "GitHub token required: set DEVORBIT_GITHUB_TOKEN on host or configure GitHub PAT in mobile Settings")
		return
	}

	reqURL := fmt.Sprintf("%s/user/repos?per_page=100&sort=updated&affiliation=owner,collaborator,organization_member", s.getGitHubAPIBaseURL())
	ghReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, reqURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to construct GitHub request")
		return
	}
	ghReq.Header.Set("Authorization", "Bearer "+token)
	ghReq.Header.Set("Accept", "application/vnd.github+json")
	ghReq.Header.Set("User-Agent", "DevOrbit-Host-Bridge/1.2.1")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(ghReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to reach GitHub API: "+err.Error())
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read GitHub API response")
		return
	}

	if resp.StatusCode != http.StatusOK {
		writeError(w, resp.StatusCode, fmt.Sprintf("GitHub API returned HTTP %d", resp.StatusCode))
		return
	}

	var ghRepos []githubAPIRepo
	if err := json.Unmarshal(bodyBytes, &ghRepos); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse GitHub repository list")
		return
	}

	existingByName := make(map[string]string)
	if s.workspaceSvc != nil {
		if workspaces, err := s.workspaceSvc.ListWorkspaces(); err == nil {
			for _, ws := range workspaces {
				existingByName[strings.ToLower(ws.Name)] = ws.RootPath
			}
		}
	}

	cloneBase := s.getGitHubCloneBaseDir()
	out := make([]map[string]any, 0, len(ghRepos))
	for _, repo := range ghRepos {
		localPath := existingByName[strings.ToLower(repo.Name)]
		isCloned := localPath != ""
		if !isCloned {
			candidate := filepath.Join(cloneBase, repo.Name)
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				isCloned = true
				localPath = candidate
				if s.workspaceSvc != nil {
					_, _ = s.workspaceSvc.RegisterWorkspace(repo.Name, candidate)
				}
			}
		}

		out = append(out, map[string]any{
			"name":           repo.Name,
			"full_name":      repo.FullName,
			"private":        repo.Private,
			"description":    repo.Description,
			"language":       repo.Language,
			"clone_url":      repo.CloneURL,
			"html_url":       repo.HTMLURL,
			"updated_at":     repo.UpdatedAt,
			"default_branch": repo.DefaultBranch,
			"is_cloned":      isCloned,
			"local_path":     localPath,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repositories": out,
		"clone_base":   cloneBase,
	})
}

func (s *Server) HandleCloneGitHubRepo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeRequest(w, r, security.PermissionGitHubClone); !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		CloneURL string `json:"clone_url"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}

	repoName := strings.TrimSpace(req.Name)
	if !isValidRepoName(repoName) {
		writeError(w, http.StatusBadRequest, "invalid repository name")
		return
	}

	parsedURL, err := url.Parse(strings.TrimSpace(req.CloneURL))
	if err != nil || strings.ToLower(parsedURL.Scheme) != "https" || parsedURL.Host == "" {
		writeError(w, http.StatusBadRequest, "clone_url must be a valid https URL")
		return
	}

	cloneBase := s.getGitHubCloneBaseDir()
	if err := os.MkdirAll(cloneBase, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare clone directory: "+err.Error())
		return
	}

	targetPath := filepath.Join(cloneBase, repoName)
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		ws, err := s.workspaceSvc.RegisterWorkspace(repoName, targetPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to register workspace: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "already_cloned",
			"workspace": ws,
		})
		return
	}

	args := []string{}
	token := s.resolveGitHubToken(r)
	var basic string
	if token != "" {
		basic = base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		args = append(args, "-c", "http.extraHeader=Authorization: Basic "+basic)
	}
	args = append(args, "clone", parsedURL.String(), targetPath)

	cmd := exec.CommandContext(r.Context(), "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(output))
		if token != "" {
			outStr = strings.ReplaceAll(outStr, token, "[REDACTED]")
		}
		if basic != "" {
			outStr = strings.ReplaceAll(outStr, basic, "[REDACTED]")
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("git clone failed: %v (%s)", err, outStr))
		return
	}

	ws, err := s.workspaceSvc.RegisterWorkspace(repoName, targetPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cloned repository but failed to register workspace: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "cloned",
		"workspace": ws,
	})
}
