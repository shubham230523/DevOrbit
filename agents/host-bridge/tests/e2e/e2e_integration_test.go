package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
)

func TestDevOrbit_FullEndToEndDevelopmentWorkflow(t *testing.T) {
	// 1. Setup temporary workstation workspace
	tempWorkspace, err := os.MkdirTemp("", "devorbit_e2e_workspace_*")
	if err != nil {
		t.Fatalf("Failed to create temp workspace: %v", err)
	}
	defer os.RemoveAll(tempWorkspace)

	cmdGit := exec.Command("git", "init")
	cmdGit.Dir = tempWorkspace
	_ = cmdGit.Run()

	manifestPath := filepath.Join(tempWorkspace, "app", "src", "main", "AndroidManifest.xml")
	_ = os.MkdirAll(filepath.Dir(manifestPath), 0755)
	_ = os.WriteFile(manifestPath, []byte("<manifest package=\"com.devorbit.test\"></manifest>"), 0644)

	buildGradlePath := filepath.Join(tempWorkspace, "build.gradle.kts")
	_ = os.WriteFile(buildGradlePath, []byte("// initial build.gradle.kts"), 0644)

	// 2. Host Bridge Security Jail & Policy
	jail := security.NewWorkspaceJail([]string{tempWorkspace})
	policy := security.NewDefaultCommandPolicy()

	// 3. Establish HKDF Directional E2E Session Keys
	e2eSession, err := security.NewE2ESession("sess-e2e-001", "phone-android-01", "master-e2e-secret-key-32-bytes!", 1*time.Hour)
	if err != nil {
		t.Fatalf("Failed to create E2E session: %v", err)
	}

	// 4. Test Host Bridge Application Services
	storageEngine := filesystem.NewStorageEngine(jail, 10*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storageEngine)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	// Step A: Discover Workspaces
	discovered, err := wsSvc.DiscoverWorkspaces(context.Background(), &domain.DiscoverOptions{SearchRoots: []string{tempWorkspace}, MaxDepth: 2})
	if err != nil || len(discovered) == 0 {
		t.Fatalf("Workspace discovery failed: %v", err)
	}

	// Step B: Read File & Verify SHA256 Hash
	readRes, err := fsSvc.ReadFile(tempWorkspace, "build.gradle.kts")
	if err != nil {
		t.Fatalf("Read file failed: %v", err)
	}
	if readRes.SHA256Hash == "" {
		t.Errorf("Expected valid SHA256 hash in read response")
	}

	// Step C: Encrypt & Decrypt Operation Request/Response using E2E Session
	plainPayload := []byte(readRes.Content)
	cipherHex, err := e2eSession.Encrypt(security.DirHostToClient, plainPayload, 1, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("Failed to encrypt E2E frame: %v", err)
	}

	decryptedPayload, err := e2eSession.Decrypt(security.DirHostToClient, cipherHex, 1, time.Now().UnixNano())
	if err != nil || string(decryptedPayload) != readRes.Content {
		t.Fatalf("E2E Decryption mismatch or tag failure: %v", err)
	}

	// Step D: Write File with SHA256 Conflict Verification
	writeReq := &domain.WriteFileRequest{
		WorkspaceID:  tempWorkspace,
		RelativePath: "build.gradle.kts",
		NewContent:   "// updated build.gradle.kts content",
		BaseSHA256:   readRes.SHA256Hash,
	}
	writeRes, err := fsSvc.WriteFile(writeReq)
	if err != nil {
		t.Fatalf("Write file failed: %v", err)
	}
	if writeRes.UpdatedSHA256 == readRes.SHA256Hash {
		t.Errorf("Expected updated SHA256 to differ from base SHA256")
	}

	// Step E: Git Status & Diff Query
	gitSt, err := gsSvc.GetStatus(context.Background(), tempWorkspace)
	if err != nil || gitSt == nil {
		t.Errorf("Git status query failed: %v", err)
	}

	// Step F: Execute Process
	procSpec := &domain.ProcessSpec{
		WorkspaceID:    tempWorkspace,
		Command:        "git",
		Args:           []string{"version"},
		TimeoutSeconds: 5,
	}
	procInfo, err := psSvc.ExecuteProcess(context.Background(), procSpec)
	if err != nil {
		t.Fatalf("Process execution failed: %v", err)
	}
	if procInfo.ID == "" {
		t.Errorf("Expected valid process ID")
	}

	// Step G: List ADB Devices
	devices, err := dsSvc.ListADBDevices(context.Background())
	if err != nil || devices == nil {
		t.Errorf("ADB device listing failed")
	}
}
