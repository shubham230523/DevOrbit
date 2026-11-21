package git

import (
	"context"
	"os/exec"
	"strings"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/security"
)

type GitClient struct {
	jail *security.WorkspaceJail
}

func NewGitClient(jail *security.WorkspaceJail) *GitClient {
	return &GitClient{jail: jail}
}

func (g *GitClient) GetStatus(ctx context.Context, workspaceID string) (*domain.GitStatus, error) {
	workDir, err := g.jail.ResolveAndValidate(workspaceID, ".")
	if err != nil {
		return nil, err
	}

	// 1. Get current branch
	cmdBranch := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmdBranch.Dir = workDir
	outBranch, err := cmdBranch.Output()
	branchName := "unknown"
	if err == nil {
		branchName = strings.TrimSpace(string(outBranch))
	}

	// 2. Get status porcelain
	cmdStatus := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmdStatus.Dir = workDir
	outStatus, err := cmdStatus.Output()
	if err != nil {
		return nil, err
	}

	staged := []string{}
	unstaged := []string{}
	untracked := []string{}

	lines := strings.Split(string(outStatus), "\n")
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		indexStatus := line[0]
		workTreeStatus := line[1]
		filePath := strings.TrimSpace(line[3:])

		if indexStatus != ' ' && indexStatus != '?' {
			staged = append(staged, filePath)
		}
		if workTreeStatus != ' ' && workTreeStatus != '?' {
			unstaged = append(unstaged, filePath)
		}
		if indexStatus == '?' && workTreeStatus == '?' {
			untracked = append(untracked, filePath)
		}
	}

	isClean := len(staged) == 0 && len(unstaged) == 0 && len(untracked) == 0

	return &domain.GitStatus{
		Branch:         branchName,
		IsClean:        isClean,
		StagedFiles:    staged,
		UnstagedFiles:  unstaged,
		UntrackedFiles: untracked,
	}, nil
}

func (g *GitClient) GetDiff(ctx context.Context, workspaceID string) (*domain.GitDiff, error) {
	workDir, err := g.jail.ResolveAndValidate(workspaceID, ".")
	if err != nil {
		return nil, err
	}

	cmdDiff := exec.CommandContext(ctx, "git", "diff", "HEAD")
	cmdDiff.Dir = workDir
	outDiff, err := cmdDiff.Output()
	if err != nil {
		// Fallback to plain git diff if HEAD ref isn't initialized yet
		cmdDiffFallback := exec.CommandContext(ctx, "git", "diff")
		cmdDiffFallback.Dir = workDir
		outDiff, _ = cmdDiffFallback.Output()
	}

	return &domain.GitDiff{
		DiffText: string(outDiff),
	}, nil
}
