//go:build e2e

// Package e2e runs the REAL broker binary as a process and exercises its transport and operational
// endpoints. It is build-tagged so the default `go test ./...` matrix stays fast; CI runs it with
// `go test -tags=e2e ./tests/e2e/`.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildBroker compiles the real broker binary once per test run.
func buildBroker(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	binaryName := "connection-broker"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(dir, binaryName)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, "./cmd/connection-broker")
	cmd.Dir = filepath.Join("..", "..")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build broker binary: %v\n%s", err, output)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("broker binary not produced: %v", err)
	}
	return binaryPath
}

// freePort reserves an ephemeral TCP port for the broker to bind.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func randomSecret(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("failed to generate secret: %v", err)
	}
	return hex.EncodeToString(buf)
}

// TestBinary_FailsClosedWithoutProductionSecret proves the real process refuses to start in
// production mode without the service-to-service credential.
func TestBinary_FailsClosedWithoutProductionSecret(t *testing.T) {
	binaryPath := buildBroker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath)
	cmd.Env = append(os.Environ(),
		"DEVORBIT_ENV=production",
		"DEVORBIT_BROKER_SECRET=",
		fmt.Sprintf("DEVORBIT_BROKER_PORT=%d", freePort(t)),
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected broker to refuse startup without DEVORBIT_BROKER_SECRET, output: %s", output)
	}
	if !strings.Contains(string(output), "DEVORBIT_BROKER_SECRET") {
		t.Fatalf("expected an explicit configuration error mentioning DEVORBIT_BROKER_SECRET, got: %s", output)
	}
}

// TestBinary_HealthMetricsAndGracefulTermination exercises the real process: liveness, readiness,
// admin-protected metrics, and termination.
func TestBinary_HealthMetricsAndGracefulTermination(t *testing.T) {
	binaryPath := buildBroker(t)
	port := freePort(t)
	secret := randomSecret(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(binaryPath)
	cmd.Env = append(os.Environ(),
		"DEVORBIT_ENV=development",
		"DEVORBIT_BROKER_SECRET="+secret,
		fmt.Sprintf("DEVORBIT_BROKER_PORT=%d", port),
	)
	var logs strings.Builder
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start broker binary: %v", err)
	}
	terminated := false
	defer func() {
		if !terminated && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	// Wait for liveness.
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(20 * time.Second)
	live := false
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/api/v1/health/live")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				live = true
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !live {
		t.Fatalf("broker did not become live; logs:\n%s", logs.String())
	}

	// Readiness must be 200 in development with a configured secret.
	readyResp, err := client.Get(baseURL + "/api/v1/health/ready")
	if err != nil {
		t.Fatalf("readiness request failed: %v", err)
	}
	_ = readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected readiness 200, got %d", readyResp.StatusCode)
	}

	// Metrics must be admin-protected.
	metricsResp, err := client.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatalf("unauthenticated metrics request failed: %v", err)
	}
	_ = metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /metrics, got %d", metricsResp.StatusCode)
	}

	// Authenticated metrics expose operational series but no credentials.
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	authResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("authenticated metrics request failed: %v", err)
	}
	body := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, readErr := authResp.Body.Read(buf)
		if n > 0 {
			body.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	_ = authResp.Body.Close()
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for authenticated /metrics, got %d", authResp.StatusCode)
	}
	if !strings.Contains(body.String(), "devorbit_broker_active_sessions") {
		t.Fatalf("expected active-session gauge in metrics output, got:\n%s", body.String())
	}
	if strings.Contains(body.String(), secret) {
		t.Fatalf("metrics output leaked the admin secret")
	}

	// Terminate the process and confirm it exits.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to terminate broker process: %v", err)
	}
	_, _ = cmd.Process.Wait()
	terminated = true
}
