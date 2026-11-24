package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestHandleListGitHubRepos_RequiresToken(t *testing.T) {
	t.Setenv("DEVORBIT_GITHUB_TOKEN", "")
	jail := security.NewWorkspaceJail([]string{t.TempDir()})
	srv := NewServer(application.NewWorkspaceService(jail), nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/github/repos", nil)
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when token missing, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleListGitHubRepos_ReturnsPublicAndPrivateAndDetectsCloned(t *testing.T) {
	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-pat-123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"name": "DevOrbit",
				"full_name": "shubham230523/DevOrbit",
				"private": false,
				"description": "Remote AI workspace",
				"language": "Go",
				"clone_url": "https://github.com/shubham230523/DevOrbit.git",
				"html_url": "https://github.com/shubham230523/DevOrbit",
				"updated_at": "2026-11-24T12:00:00Z",
				"default_branch": "main"
			},
			{
				"name": "SecretProject",
				"full_name": "shubham230523/SecretProject",
				"private": true,
				"description": "Private repo",
				"language": "Kotlin",
				"clone_url": "https://github.com/shubham230523/SecretProject.git",
				"html_url": "https://github.com/shubham230523/SecretProject",
				"updated_at": "2026-11-24T11:00:00Z",
				"default_branch": "main"
			}
		]`))
	}))
	defer mockGH.Close()

	cloneBase := t.TempDir()
	// Pre-create DevOrbit/.git so it is detected as already cloned
	if err := os.MkdirAll(filepath.Join(cloneBase, "DevOrbit", ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	jail := security.NewWorkspaceJail([]string{})
	srv := NewServer(application.NewWorkspaceService(jail), nil, nil, nil, nil)
	srv.githubAPIBaseURL = mockGH.URL
	srv.githubCloneBaseDir = cloneBase

	req := httptest.NewRequest(http.MethodGet, "/api/v1/github/repos", nil)
	req.Header.Set("X-GitHub-Token", "test-pat-123")
	rec := httptest.NewRecorder()
	srv.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Repositories []map[string]any `json:"repositories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(resp.Repositories))
	}
	if resp.Repositories[0]["is_cloned"] != true {
		t.Fatalf("expected DevOrbit to be marked is_cloned=true")
	}
	if resp.Repositories[1]["private"] != true || resp.Repositories[1]["is_cloned"] != false {
		t.Fatalf("expected SecretProject to be private=true and is_cloned=false")
	}
}

func TestHandleCloneGitHubRepo_ValidationAndAlreadyCloned(t *testing.T) {
	cloneBase := t.TempDir()
	jail := security.NewWorkspaceJail([]string{})
	wsSvc := application.NewWorkspaceService(jail)
	srv := NewServer(wsSvc, nil, nil, nil, nil)
	srv.githubCloneBaseDir = cloneBase

	// 1. Reject path traversal repo name
	reqBadName := httptest.NewRequest(http.MethodPost, "/api/v1/github/clone",
		bytes.NewBufferString(`{"name":"../evil","clone_url":"https://github.com/a/b.git"}`))
	recBadName := httptest.NewRecorder()
	srv.Mux().ServeHTTP(recBadName, reqBadName)
	if recBadName.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid repo name, got %d", recBadName.Code)
	}

	// 2. Reject non-https URL
	reqBadURL := httptest.NewRequest(http.MethodPost, "/api/v1/github/clone",
		bytes.NewBufferString(`{"name":"MyRepo","clone_url":"http://github.com/a/b.git"}`))
	recBadURL := httptest.NewRecorder()
	srv.Mux().ServeHTTP(recBadURL, reqBadURL)
	if recBadURL.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-https clone_url, got %d", recBadURL.Code)
	}

	// 3. Existing .git directory registers workspace cleanly without running git clone
	existingRepo := filepath.Join(cloneBase, "ExistingRepo")
	if err := os.MkdirAll(filepath.Join(existingRepo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	reqExisting := httptest.NewRequest(http.MethodPost, "/api/v1/github/clone",
		bytes.NewBufferString(`{"name":"ExistingRepo","clone_url":"https://github.com/shubham230523/ExistingRepo.git"}`))
	recExisting := httptest.NewRecorder()
	srv.Mux().ServeHTTP(recExisting, reqExisting)
	if recExisting.Code != http.StatusOK {
		t.Fatalf("expected 200 for already_cloned, got %d: %s", recExisting.Code, recExisting.Body.String())
	}

	workspaces, err := wsSvc.ListWorkspaces()
	if err != nil || len(workspaces) != 1 || workspaces[0].Name != "ExistingRepo" {
		t.Fatalf("expected ExistingRepo registered in WorkspaceService, got %+v (err=%v)", workspaces, err)
	}
}
