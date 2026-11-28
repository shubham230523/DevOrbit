package http_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/security"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
)

// approvalE2E drives the process-approval lifecycle over TLS with cryptographically signed requests,
// using the real authentication middleware rather than calling handlers directly.
type approvalE2E struct {
	t        *testing.T
	env      *secTestEnv
	creds    *secSessionCreds
	client   *http.Client
	nonceSeq int64
}

func newApprovalE2E(t *testing.T) *approvalE2E {
	t.Helper()
	env := newSecTestEnv(t)
	creds := env.registerSession("sess-approval-e2e", "dev-approval-e2e", "token-approval-e2e-0123456789",
		"master-secret-approval-e2e-0123456789", time.Hour, security.NewFullSessionPermissions())

	// A real, harmless executable named like a Gradle wrapper so the approval gate can be exercised
	// end to end without running any build script.
	writeFakeGradle(t, env.workspace)

	return &approvalE2E{t: t, env: env, creds: creds, client: env.tlsServer.Client()}
}

// writeFakeGradle creates the workspace wrapper that these tests execute. The realistic invocation is
// the workspace-relative wrapper (`./gradlew`), which is also the form that exercises real-path
// containment resolution.
func writeFakeGradle(t *testing.T, workspace string) {
	t.Helper()
	target := filepath.Join(workspace, gradleWrapperName())
	if runtime.GOOS == "windows" {
		// Windows needs a PE image for a direct execution. A copy of cmd.exe exits immediately on the
		// bogus task argument, which is enough to prove the approved process was launched.
		source := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("failed to read %s: %v", source, err)
		}
		if err := os.WriteFile(target, data, 0o700); err != nil {
			t.Fatalf("failed to write fake gradlew: %v", err)
		}
		return
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("failed to write fake gradlew: %v", err)
	}
}

// gradleWrapperName is the fixture executable name. A real project ships `gradlew.bat` on Windows,
// which cannot be executed directly without cmd.exe, so the fixture uses the allowlisted `gradle`
// name instead; the approval gate is family-wide and does not depend on the wrapper spelling.
func gradleWrapperName() string {
	if runtime.GOOS == "windows" {
		return "gradle.exe"
	}
	return "gradle"
}

// gradleCommand returns the command form used by these tests: the workspace-relative wrapper.
func gradleCommand() string {
	return "./" + gradleWrapperName()
}

func (e *approvalE2E) nextNonce(prefix string) string {
	seq := atomic.AddInt64(&e.nonceSeq, 1)
	return fmt.Sprintf("%s-%s-%d", prefix, e.creds.sessionID, seq)
}

// do sends a signed HTTPS request with the primary session credentials.
func (e *approvalE2E) do(method, path string, body []byte) (int, string) {
	e.t.Helper()
	return e.doAs(e.creds, method, path, body, nil)
}

func (e *approvalE2E) doAs(creds *secSessionCreds, method, path string, body []byte, tweak func(*http.Request)) (int, string) {
	e.t.Helper()
	resp := doTLSRequest(e.t, e.client, e.env.tlsServer.URL, method, path, body, func(r *http.Request) {
		signTLSHeaders(r, body, creds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), e.nextNonce("approval"))
		if tweak != nil {
			tweak(r)
		}
	})
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func decodeJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, raw)
	}
	return payload
}

func (e *approvalE2E) requestApproval(command string, args []string) map[string]any {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"workspace_id": e.env.workspace,
		"command":      command,
		"args":         args,
	})
	status, raw := e.do(http.MethodPost, "/api/v1/processes/approvals", body)
	if status != http.StatusAccepted {
		e.t.Fatalf("expected 202 from approval request, got %d (%s)", status, raw)
	}
	return decodeJSON(e.t, raw)
}

func (e *approvalE2E) decide(approvalID, decision, digest string) (int, string) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"decision": decision, "command_digest": digest})
	return e.do(http.MethodPost, "/api/v1/processes/approvals/"+approvalID+"/decision", body)
}

func (e *approvalE2E) execute(command string, args []string, token string) (int, string) {
	e.t.Helper()
	payload := map[string]any{"workspace_id": e.env.workspace, "command": command, "args": args}
	if token != "" {
		payload["approval_token"] = token
	}
	body, _ := json.Marshal(payload)
	return e.do(http.MethodPost, "/api/v1/processes/execute", body)
}

// --- Scenario 1-3: authentication of the approval surface ------------------------------

func TestApprovalE2E_UnauthenticatedApprovalRequestRejected(t *testing.T) {
	env := newSecTestEnv(t)
	body, _ := json.Marshal(map[string]any{
		"workspace_id": env.workspace, "command": gradleCommand(), "args": []string{"build"},
	})
	resp := doTLSRequest(t, env.tlsServer.Client(), env.tlsServer.URL, http.MethodPost, "/api/v1/processes/approvals", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", resp.StatusCode)
	}
}

func TestApprovalE2E_InvalidSignatureAndStaleTimestampRejected(t *testing.T) {
	e := newApprovalE2E(t)
	body, _ := json.Marshal(map[string]any{
		"workspace_id": e.env.workspace, "command": gradleCommand(), "args": []string{"build"},
	})

	status, _ := e.doAs(e.creds, http.MethodPost, "/api/v1/processes/approvals", body, func(r *http.Request) {
		r.Header.Set(httptransport.HeaderSignature, "deadbeef")
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an invalid signature, got %d", status)
	}

	staleTS := strconv.FormatInt(time.Now().UTC().Add(-15*time.Minute).UnixMicro(), 10)
	resp := doTLSRequest(t, e.client, e.env.tlsServer.URL, http.MethodPost, "/api/v1/processes/approvals", body, func(r *http.Request) {
		signTLSHeaders(r, body, e.creds, staleTS, e.nextNonce("stale"))
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a stale timestamp, got %d", resp.StatusCode)
	}
}

// --- Scenario 4-6: the MP26.1 self-approval flaw ---------------------------------------

// TestApprovalE2E_RequestGrantsNoExecutionCapability is the regression test for the MP26.1 flaw:
// creating an approval must not return anything that can execute the command.
func TestApprovalE2E_RequestGrantsNoExecutionCapability(t *testing.T) {
	e := newApprovalE2E(t)
	proposal := e.requestApproval(gradleCommand(), []string{"build"})

	if _, ok := proposal["execution_token"]; ok {
		t.Fatalf("approval request must not return an execution token: %v", proposal)
	}
	if proposal["state"] != string(application.ProcessApprovalPending) {
		t.Fatalf("expected pending state, got %v", proposal["state"])
	}

	// Attempting to execute with the approval id (the strongest thing the requester holds) fails.
	approvalID, _ := proposal["approval_id"].(string)
	if status, raw := e.execute(gradleCommand(), []string{"build"}, approvalID); status != http.StatusForbidden {
		t.Fatalf("expected 403 when using the approval id as a token, got %d (%s)", status, raw)
	}

	// Attempting to execute with no token at all also fails.
	if status, raw := e.execute(gradleCommand(), []string{"build"}, ""); status != http.StatusForbidden {
		t.Fatalf("expected 403 without any token, got %d (%s)", status, raw)
	}
}

// TestApprovalE2E_AuthenticatedSessionAloneCannotExecute proves authentication is not authorization
// for code-executing commands.
func TestApprovalE2E_AuthenticatedSessionAloneCannotExecute(t *testing.T) {
	e := newApprovalE2E(t)

	status, raw := e.execute(gradleCommand(), []string{"assembleDebug"}, "")
	if status != http.StatusForbidden || !strings.Contains(raw, "approval_required") {
		t.Fatalf("expected 403 approval_required, got %d (%s)", status, raw)
	}
}

// TestApprovalE2E_ApprovalRequiresDigestOfTheShownProposal proves a decision cannot be recorded
// without referencing the exact proposal content the host issued.
func TestApprovalE2E_ApprovalRequiresDigestOfTheShownProposal(t *testing.T) {
	e := newApprovalE2E(t)
	proposal := e.requestApproval(gradleCommand(), []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)

	status, raw := e.decide(approvalID, "approve", "")
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 without a digest, got %d (%s)", status, raw)
	}
	status, raw = e.decide(approvalID, "approve", "0000000000000000000000000000000000000000000000000000000000000000")
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for a wrong digest, got %d (%s)", status, raw)
	}

	// The proposal is still pending and still grants nothing.
	status, raw = e.execute(gradleCommand(), []string{"build"}, "")
	if status != http.StatusForbidden {
		t.Fatalf("expected still-unauthorized execution, got %d (%s)", status, raw)
	}
}

// --- Scenario 7-13: decision semantics -------------------------------------------------

func TestApprovalE2E_ExplicitApprovalPermitsExactlyTheApprovedCommand(t *testing.T) {
	e := newApprovalE2E(t)
	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)
	digest, _ := proposal["command_digest"].(string)

	status, raw := e.decide(approvalID, "approve", digest)
	if status != http.StatusOK {
		t.Fatalf("expected 200 from approval decision, got %d (%s)", status, raw)
	}
	decision := decodeJSON(t, raw)
	token, _ := decision["execution_token"].(string)
	if token == "" {
		t.Fatalf("expected an execution token after approval, got %s", raw)
	}

	status, raw = e.execute(command, []string{"build"}, token)
	if status != http.StatusOK {
		t.Fatalf("expected approved execution to start, got %d (%s)", status, raw)
	}

	// The token is single use.
	if status, raw := e.execute(command, []string{"build"}, token); status != http.StatusForbidden {
		t.Fatalf("expected replay rejection, got %d (%s)", status, raw)
	}
}

func TestApprovalE2E_RejectionPreventsExecution(t *testing.T) {
	e := newApprovalE2E(t)
	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"assembleRelease"})
	approvalID, _ := proposal["approval_id"].(string)

	status, raw := e.decide(approvalID, "reject", "")
	if status != http.StatusOK {
		t.Fatalf("expected 200 from rejection, got %d (%s)", status, raw)
	}
	if decision := decodeJSON(t, raw); decision["state"] != string(application.ProcessApprovalRejected) {
		t.Fatalf("expected rejected state, got %v", decision["state"])
	}

	// No token can be obtained afterwards, and execution stays blocked.
	digest, _ := proposal["command_digest"].(string)
	if status, _ := e.decide(approvalID, "approve", digest); status != http.StatusConflict {
		t.Fatalf("expected 409 when approving a rejected proposal, got %d", status)
	}
	if status, raw := e.execute(command, []string{"assembleRelease"}, ""); status != http.StatusForbidden {
		t.Fatalf("expected execution to remain blocked, got %d (%s)", status, raw)
	}
}

func TestApprovalE2E_ModifiedArgumentsOrWorkspaceInvalidateApproval(t *testing.T) {
	e := newApprovalE2E(t)
	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)
	digest, _ := proposal["command_digest"].(string)

	status, raw := e.decide(approvalID, "approve", digest)
	if status != http.StatusOK {
		t.Fatalf("expected approval to succeed, got %d (%s)", status, raw)
	}
	token, _ := decodeJSON(t, raw)["execution_token"].(string)

	// Extra argument changes the execution request.
	extraArgs, _ := json.Marshal(map[string]any{
		"workspace_id": e.env.workspace, "command": command,
		"args": []string{"build", "--offline"}, "approval_token": token,
	})
	if status, raw := e.do(http.MethodPost, "/api/v1/processes/execute", extraArgs); status != http.StatusForbidden {
		t.Fatalf("expected modified arguments to be rejected, got %d (%s)", status, raw)
	}

	// Different workspace changes the execution context.
	otherWorkspace, _ := json.Marshal(map[string]any{
		"workspace_id": "/somewhere/else", "command": command,
		"args": []string{"build"}, "approval_token": token,
	})
	if status, raw := e.do(http.MethodPost, "/api/v1/processes/execute", otherWorkspace); status != http.StatusForbidden {
		t.Fatalf("expected modified workspace to be rejected, got %d (%s)", status, raw)
	}

	// The original request still works exactly once.
	if status, raw := e.execute(command, []string{"build"}, token); status != http.StatusOK {
		t.Fatalf("expected the untouched request to still execute, got %d (%s)", status, raw)
	}
}

func TestApprovalE2E_DifferentSessionCannotDecideOrConsume(t *testing.T) {
	e := newApprovalE2E(t)
	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)
	digest, _ := proposal["command_digest"].(string)

	other := e.env.registerSession("sess-approval-other", "dev-approval-e2e", "token-approval-other-0123456789",
		"master-secret-approval-other-0123456789", time.Hour, security.NewFullSessionPermissions())

	// Another session cannot read the proposal.
	if status, raw := e.doAs(other, http.MethodGet, "/api/v1/processes/approvals/"+approvalID, nil, nil); status != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-session status read, got %d (%s)", status, raw)
	}

	// Another session cannot decide it.
	body, _ := json.Marshal(map[string]any{"decision": "approve", "command_digest": digest})
	if status, raw := e.doAs(other, http.MethodPost, "/api/v1/processes/approvals/"+approvalID+"/decision", body, nil); status != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-session decision, got %d (%s)", status, raw)
	}

	// The owner can still approve, but the other session cannot use the resulting token.
	status, raw := e.decide(approvalID, "approve", digest)
	if status != http.StatusOK {
		t.Fatalf("expected owner approval to succeed, got %d (%s)", status, raw)
	}
	token, _ := decodeJSON(t, raw)["execution_token"].(string)

	payload, _ := json.Marshal(map[string]any{
		"workspace_id": e.env.workspace, "command": command, "args": []string{"build"}, "approval_token": token,
	})
	if status, raw := e.doAs(other, http.MethodPost, "/api/v1/processes/execute", payload, nil); status != http.StatusForbidden {
		t.Fatalf("expected 403 when another session uses the token, got %d (%s)", status, raw)
	}
}

func TestApprovalE2E_ExpiredApprovalIsRejected(t *testing.T) {
	e := newApprovalE2E(t)
	// Inject a store whose proposals lapse immediately.
	e.env.server.SetProcessApprovalStore(application.NewProcessApprovalStore(time.Millisecond))

	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)
	digest, _ := proposal["command_digest"].(string)

	time.Sleep(10 * time.Millisecond)

	status, raw := e.decide(approvalID, "approve", digest)
	if status != http.StatusGone {
		t.Fatalf("expected 410 for an expired proposal, got %d (%s)", status, raw)
	}
}

func TestApprovalE2E_RevokedSessionCannotConsume(t *testing.T) {
	e := newApprovalE2E(t)
	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)
	digest, _ := proposal["command_digest"].(string)

	status, raw := e.decide(approvalID, "approve", digest)
	if status != http.StatusOK {
		t.Fatalf("expected approval to succeed, got %d (%s)", status, raw)
	}
	token, _ := decodeJSON(t, raw)["execution_token"].(string)

	if _, err := e.env.server.SessionStore().RevokeSession(e.creds.sessionID); err != nil {
		t.Fatalf("failed to revoke session: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"workspace_id": e.env.workspace, "command": command, "args": []string{"build"}, "approval_token": token,
	})
	status, raw = e.do(http.MethodPost, "/api/v1/processes/execute", payload)
	if status == http.StatusOK {
		t.Fatalf("expected a revoked session to be unable to execute, got 200 (%s)", raw)
	}
}

// doConcurrent performs a signed request without failing the test from a non-test goroutine.
func (e *approvalE2E) doConcurrent(method, path string, body []byte) (int, error) {
	req, err := http.NewRequest(method, e.env.tlsServer.URL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	signTLSHeaders(req, body, e.creds, strconv.FormatInt(time.Now().UTC().UnixMicro(), 10), e.nextNonce("concurrent"))
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func TestApprovalE2E_ConcurrentDuplicateExecutionRunsOnce(t *testing.T) {
	e := newApprovalE2E(t)
	command := gradleCommand()
	proposal := e.requestApproval(command, []string{"build"})
	approvalID, _ := proposal["approval_id"].(string)
	digest, _ := proposal["command_digest"].(string)

	status, raw := e.decide(approvalID, "approve", digest)
	if status != http.StatusOK {
		t.Fatalf("expected approval to succeed, got %d (%s)", status, raw)
	}
	token, _ := decodeJSON(t, raw)["execution_token"].(string)

	payload, _ := json.Marshal(map[string]any{
		"workspace_id": e.env.workspace, "command": command, "args": []string{"build"}, "approval_token": token,
	})

	var successes int64
	var transportErrors int64
	var mu sync.Mutex
	var statuses []int
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := e.doConcurrent(http.MethodPost, "/api/v1/processes/execute", payload)
			if err != nil {
				atomic.AddInt64(&transportErrors, 1)
				return
			}
			mu.Lock()
			statuses = append(statuses, status)
			mu.Unlock()
			if status == http.StatusOK {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&transportErrors); got != 0 {
		t.Fatalf("expected no transport errors during concurrent execution, got %d (statuses: %v)", got, statuses)
	}
	if got := atomic.LoadInt64(&successes); got != 1 {
		t.Fatalf("expected exactly one successful execution, got %d (statuses: %v)", got, statuses)
	}
}

// --- Scenario 16: no regression for commands that do not need approval ------------------

func TestApprovalE2E_NonGradleCommandsUnaffected(t *testing.T) {
	e := newApprovalE2E(t)

	status, raw := e.execute("git", []string{"version"}, "")
	if status != http.StatusOK {
		t.Fatalf("expected git version to run without approval, got %d (%s)", status, raw)
	}
	if strings.Contains(raw, "approval_required") {
		t.Fatalf("non-Gradle command must not be approval gated: %s", raw)
	}

	// Requesting an approval for a command that needs none is refused rather than silently accepted.
	body, _ := json.Marshal(map[string]any{"workspace_id": e.env.workspace, "command": "git", "args": []string{"status"}})
	if status, raw := e.do(http.MethodPost, "/api/v1/processes/approvals", body); status != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-approval command, got %d (%s)", status, raw)
	}
}
