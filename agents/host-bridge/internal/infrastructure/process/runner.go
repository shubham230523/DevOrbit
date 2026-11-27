package process

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

type ProcessRunner struct {
	policy    *security.CommandPolicy
	jail      *security.WorkspaceJail
	mu        sync.RWMutex
	processes map[string]*activeProcess
}

type activeProcess struct {
	info   *domain.ProcessInfo
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

func NewProcessRunner(policy *security.CommandPolicy, jail *security.WorkspaceJail) *ProcessRunner {
	return &ProcessRunner{
		policy:    policy,
		jail:      jail,
		processes: make(map[string]*activeProcess),
	}
}

func (r *ProcessRunner) ExecuteProcess(ctx context.Context, spec *domain.ProcessSpec) (*domain.ProcessInfo, error) {
	if err := r.policy.ValidateCommand(spec.Command, spec.Args); err != nil {
		return nil, err
	}

	workDir, err := r.jail.ResolveAndValidate(spec.WorkspaceID, ".")
	if err != nil {
		return nil, err
	}

	timeoutSec := spec.TimeoutSeconds
	if timeoutSec <= 0 || timeoutSec > 300 {
		timeoutSec = 60
	}

	pCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)

	cmd := exec.CommandContext(pCtx, spec.Command, spec.Args...)
	cmd.Dir = workDir

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	processID := fmt.Sprintf("proc-%d", time.Now().UnixNano())
	nowStr := time.Now().Format("2006-01-02T15:04:05Z07:00")

	procInfo := &domain.ProcessInfo{
		ID:        processID,
		Command:   spec.Command,
		Args:      spec.Args,
		Status:    domain.ProcessStatusRunning,
		StartTime: nowStr,
	}

	ap := &activeProcess{
		info:   procInfo,
		cancel: cancel,
		cmd:    cmd,
	}

	r.mu.Lock()
	if len(r.processes) > 200 {
		for id, p := range r.processes {
			if p.info.Status != domain.ProcessStatusRunning {
				delete(r.processes, id)
				if len(r.processes) <= 100 {
					break
				}
			}
		}
	}
	r.processes[processID] = ap
	r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.processes, processID)
		r.mu.Unlock()
		return nil, fmt.Errorf("failed to start process '%s': %w", spec.Command, err)
	}

	go func() {
		err := cmd.Wait()
		ap.cancel()

		r.mu.Lock()
		defer r.mu.Unlock()

		ap.info.EndTime = time.Now().Format("2006-01-02T15:04:05Z07:00")
		ap.info.Stdout = stdoutBuf.String()
		ap.info.Stderr = stderrBuf.String()

		if pCtx.Err() == context.DeadlineExceeded {
			ap.info.Status = domain.ProcessStatusCancelled
			ap.info.ExitCode = -1
		} else if err != nil {
			ap.info.Status = domain.ProcessStatusFailed
			if exitErr, ok := err.(*exec.ExitError); ok {
				ap.info.ExitCode = exitErr.ExitCode()
			} else {
				ap.info.ExitCode = 1
			}
		} else {
			ap.info.Status = domain.ProcessStatusCompleted
			ap.info.ExitCode = 0
		}
	}()

	return procInfo, nil
}

func (r *ProcessRunner) GetProcessStatus(id string) (*domain.ProcessInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ap, ok := r.processes[id]
	if !ok {
		return nil, false
	}
	return ap.info, true
}

func (r *ProcessRunner) CancelProcess(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	ap, ok := r.processes[id]
	if !ok {
		return false
	}

	if ap.cancel != nil {
		ap.cancel()
	}
	if ap.cmd != nil && ap.cmd.Process != nil {
		_ = ap.cmd.Process.Kill()
	}
	ap.info.Status = domain.ProcessStatusCancelled
	return true
}
