package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestTransport_HTTPAPIEndpoints(t *testing.T) {
	tempRoot, err := os.MkdirTemp("", "devorbit_transport_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempRoot)

	jail := security.NewWorkspaceJail([]string{tempRoot})
	policy := security.NewDefaultCommandPolicy()

	storage := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storage)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	server := NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Test Health Endpoint
	res, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("Health check failed: %v", err)
	}

	// 2. Test Write File Endpoint
	writeReq := map[string]string{
		"workspace_id":  tempRoot,
		"relative_path": "settings.gradle.kts",
		"new_content":   "// devorbit settings",
		"base_sha256":   "",
	}
	reqBody, _ := json.Marshal(writeReq)

	resWrite, err := http.Post(ts.URL+"/api/v1/files/write", "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resWrite.StatusCode != http.StatusOK {
		t.Fatalf("Write file API failed: %v", err)
	}

	// 3. Test Read File Endpoint
	readReq := map[string]string{
		"workspace_id":  tempRoot,
		"relative_path": "settings.gradle.kts",
	}
	reqReadBody, _ := json.Marshal(readReq)
	resRead, err := http.Post(ts.URL+"/api/v1/files/read", "application/json", bytes.NewBuffer(reqReadBody))
	if err != nil || resRead.StatusCode != http.StatusOK {
		t.Fatalf("Read file API failed: %v", err)
	}
}
