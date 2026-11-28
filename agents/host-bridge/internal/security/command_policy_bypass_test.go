package security

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"devorbit.net/agents/host-bridge/internal/domain"
)

// TestCommandPolicy_RejectsAttachedShortOptionValues covers the classic token-matching bypass:
// denying the token "-c" does not deny "-cfoo=bar", because Git accepts a value attached directly to
// the option letter. The same applies to Gradle's -I/-b/-c/-p/-g.
func TestCommandPolicy_RejectsAttachedShortOptionValues(t *testing.T) {
	p := NewDefaultCommandPolicy()

	rejected := []struct {
		command string
		args    []string
		reason  string
	}{
		// Git: configuration injection with an attached value.
		{"git", []string{"-ccore.pager=sh", "status"}, "attached -c value"},
		{"git", []string{"-c", "core.pager=sh", "status"}, "separate -c value"},
		// Git: repository relocation with an attached value.
		{"git", []string{"-C/etc", "status"}, "attached -C value"},
		{"git", []string{"-C", "/etc", "status"}, "separate -C value"},
		// Gradle: init script, build file, settings file, project dir and user home.
		{"gradlew", []string{"-Ievil.gradle", "build"}, "attached -I value"},
		{"gradle", []string{"-I", "evil.gradle", "build"}, "separate -I value"},
		{"gradlew", []string{"-bbuild.gradle", "build"}, "attached -b value"},
		{"gradlew", []string{"-cevil.settings", "build"}, "attached -c value"},
		{"gradlew", []string{"-p/tmp/other", "build"}, "attached -p value"},
		{"gradlew", []string{"-g/tmp/gradle-home", "build"}, "attached -g value"},
		// Clustered short options must not hide a denied letter.
		{"gradlew", []string{"-IPevil.gradle", "build"}, "clustered -I"},
	}

	for _, tc := range rejected {
		if err := p.ValidateCommand(tc.command, tc.args); err == nil {
			t.Fatalf("%s: expected rejection of %q %v", tc.reason, tc.command, tc.args)
		} else if !isPolicyError(err) {
			t.Fatalf("%s: expected ErrCommandNotAllowed, got %v", tc.reason, err)
		}
	}
}

// TestCommandPolicy_RejectsLongOptionValueForms covers `--option=value` and `--option value`, which
// a prefix list alone would miss for equivalent spellings.
func TestCommandPolicy_RejectsLongOptionValueForms(t *testing.T) {
	p := NewDefaultCommandPolicy()

	cases := []struct {
		command string
		args    []string
		reason  string
	}{
		{"git", []string{"--git-dir=/tmp/evil", "status"}, "git-dir with value"},
		{"git", []string{"--git-dir", "/tmp/evil", "status"}, "git-dir separate value"},
		{"git", []string{"--work-tree=/tmp/evil", "status"}, "work-tree"},
		{"git", []string{"--config-env=core.pager=E", "status"}, "config-env"},
		{"git", []string{"--upload-pack=/bin/sh", "fetch"}, "upload-pack"},
		{"git", []string{"--receive-pack=/bin/sh", "push"}, "receive-pack"},
		{"git", []string{"--exec-path=/tmp/evil", "status"}, "exec-path"},
		{"git", []string{"--open-files-in-pager=/bin/sh", "log"}, "pager"},
		{"git", []string{"--no-index", "diff", "a", "b"}, "no-index escapes the repository"},
		{"git", []string{"--output=/etc/cron.d/x", "log"}, "arbitrary file write"},
		{"git", []string{"--ext-diff", "log"}, "external diff driver"},
		{"git", []string{"--textconv", "show"}, "textconv driver"},
		{"gradlew", []string{"--init-script=evil.gradle", "build"}, "init-script with value"},
		{"gradlew", []string{"--init-script", "evil.gradle", "build"}, "init-script separate value"},
		{"gradlew", []string{"--build-file=evil.gradle", "build"}, "build-file"},
		{"gradlew", []string{"--settings-file=evil.settings", "build"}, "settings-file"},
		{"gradlew", []string{"--project-dir=/tmp/other", "build"}, "project-dir"},
		{"gradlew", []string{"--include-build=/tmp/other", "build"}, "include-build"},
		{"gradlew", []string{"--gradle-user-home=/tmp/x", "build"}, "gradle-user-home"},
		{"gradlew", []string{"--project-cache-dir=/tmp/x", "build"}, "project-cache-dir"},
	}

	for _, tc := range cases {
		if err := p.ValidateCommand(tc.command, tc.args); err == nil {
			t.Fatalf("%s: expected rejection of %q %v", tc.reason, tc.command, tc.args)
		} else if !isPolicyError(err) {
			t.Fatalf("%s: expected ErrCommandNotAllowed, got %v", tc.reason, err)
		}
	}
}

// TestCommandPolicy_OptionCaseSensitivity guards the semantics that make cluster scanning safe:
// Gradle -i (info) is legitimate while -I (init script) is not.
func TestCommandPolicy_OptionCaseSensitivity(t *testing.T) {
	p := NewDefaultCommandPolicy()

	if err := p.ValidateCommand("gradlew", []string{"-i", "build"}); err != nil {
		t.Fatalf("expected gradle -i (info) to remain allowed, got: %v", err)
	}
	if err := p.ValidateCommand("gradlew", []string{"--info", "build"}); err != nil {
		t.Fatalf("expected gradle --info to remain allowed, got: %v", err)
	}
	if err := p.ValidateCommand("gradlew", []string{"-I", "evil.gradle", "build"}); err == nil {
		t.Fatalf("expected gradle -I (init script) to be rejected")
	}
}

// TestCommandPolicy_LegitimateValuesContainingOptionLetters ensures structural parsing does not
// misread option-like text inside an option value (a commit message mentioning "-c", for example).
func TestCommandPolicy_LegitimateValuesContainingOptionLetters(t *testing.T) {
	p := NewDefaultCommandPolicy()

	allowed := []struct {
		args []string
	}{
		{[]string{"commit", "-m", "fix -c handling in command policy"}},
		{[]string{"commit", "-m", "document -I and -C flags"}},
		{[]string{"log", "-n", "5", "--oneline"}},
		{[]string{"log", "-5", "--oneline"}},
		{[]string{"diff", "--stat"}},
		{[]string{"status", "--porcelain"}},
		{[]string{"stash", "list"}},
	}
	for _, tc := range allowed {
		if err := p.ValidateCommand("git", tc.args); err != nil {
			t.Fatalf("expected git %v to remain allowed, got: %v", tc.args, err)
		}
	}

	if err := p.ValidateCommand("gradlew", []string{"assembleRelease", "-PversionName=1.2.3"}); err != nil {
		t.Fatalf("expected gradle property override to remain allowed, got: %v", err)
	}
	if err := p.ValidateCommand("gradlew", []string{"-Dorg.gradle.parallel=true", "build"}); err != nil {
		t.Fatalf("expected gradle system property to remain allowed, got: %v", err)
	}
}

// TestCommandPolicy_ArgumentOrderVariations ensures option position cannot change the outcome.
func TestCommandPolicy_ArgumentOrderVariations(t *testing.T) {
	p := NewDefaultCommandPolicy()

	// Denied option before, between and after other arguments is rejected in every position.
	orders := [][]string{
		{"-c", "core.pager=sh", "status"},
		{"status", "src/main.kt", "-c", "core.pager=sh"},
		{"--no-pager", "-c", "core.pager=sh", "status"},
	}
	for _, args := range orders {
		if err := p.ValidateCommand("git", args); err == nil {
			t.Fatalf("expected rejection regardless of option order: %v", args)
		}
	}
}

// TestCommandPolicy_RequiresApproval_Gradle verifies the trust-boundary classification: Gradle runs
// project-defined code, so it always needs an approval decision; git/adb do not.
func TestCommandPolicy_RequiresApproval_Gradle(t *testing.T) {
	p := NewDefaultCommandPolicy()

	for _, command := range []string{"gradlew", "gradle", "gradlew.bat", "gradle.exe", "./gradlew"} {
		required, reason := p.RequiresApproval(command, []string{"build"})
		if !required {
			t.Fatalf("expected %s execution to require approval", command)
		}
		if reason == "" {
			t.Fatalf("expected a non-empty approval reason for %s", command)
		}
	}

	for _, command := range []string{"git", "git.exe", "adb", "adb.exe"} {
		if required, _ := p.RequiresApproval(command, []string{"status"}); required {
			t.Fatalf("expected %s not to require approval", command)
		}
	}

	var nilPolicy *CommandPolicy
	if required, _ := nilPolicy.RequiresApproval("git", []string{"status"}); !required {
		t.Fatalf("nil policy must fail closed and require approval")
	}
}

// TestResolveExecutable_SymlinkContainment proves containment is decided on the real filesystem.
//
// Every case uses the allowlisted wrapper name `gradlew`, so the executable-identity check cannot be
// what rejects the request: only real-path containment can decide the outcome.
func TestResolveExecutable_SymlinkContainment(t *testing.T) {
	p := NewDefaultCommandPolicy()

	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	outsideBinary := filepath.Join(outside, "evil")
	if err := os.WriteFile(outsideBinary, []byte("#!/bin/sh\necho pwned\n"), 0o700); err != nil {
		t.Fatalf("failed to write outside executable: %v", err)
	}

	makeWorkspace := func(t *testing.T) string {
		t.Helper()
		ws, err := os.MkdirTemp(root, "ws-")
		if err != nil {
			t.Fatalf("failed to create workspace: %v", err)
		}
		return ws
	}

	// 1. Normal executable inside the workspace resolves.
	regular := makeWorkspace(t)
	if err := os.WriteFile(filepath.Join(regular, "gradlew"), []byte("#!/bin/sh\necho ok\n"), 0o700); err != nil {
		t.Fatalf("failed to write executable: %v", err)
	}
	resolved, err := p.ResolveExecutable("./gradlew", regular)
	if err != nil {
		t.Fatalf("expected in-workspace executable to resolve, got: %v", err)
	}
	if !isWithin(mustEval(t, regular), resolved) {
		t.Fatalf("resolved executable is not inside the workspace: %s", resolved)
	}

	// Symlink creation may require privileges (Windows without Developer Mode). Skip the symlink
	// cases there and report the platform limitation instead of pretending coverage.
	probe := filepath.Join(regular, "probe")
	if err := os.Symlink(filepath.Join(regular, "gradlew"), probe); err != nil {
		t.Skipf("symlink creation unavailable on %s: %v", runtime.GOOS, err)
	}
	_ = os.Remove(probe)

	// 2. Wrapper symlinked outside the workspace is rejected.
	escaping := makeWorkspace(t)
	if err := os.Symlink(outsideBinary, filepath.Join(escaping, "gradlew")); err != nil {
		t.Fatalf("failed to create escaping symlink: %v", err)
	}
	if _, err := p.ResolveExecutable("./gradlew", escaping); err == nil {
		t.Fatalf("expected wrapper symlinked outside the workspace to be rejected")
	}

	// 3. Wrapper symlinked to a file inside the workspace is accepted.
	inside := makeWorkspace(t)
	if err := os.WriteFile(filepath.Join(inside, "real-gradlew"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("failed to write real wrapper: %v", err)
	}
	if err := os.Symlink(filepath.Join(inside, "real-gradlew"), filepath.Join(inside, "gradlew")); err != nil {
		t.Fatalf("failed to create inside symlink: %v", err)
	}
	if _, err := p.ResolveExecutable("./gradlew", inside); err != nil {
		t.Fatalf("expected in-workspace symlink to resolve, got: %v", err)
	}

	// 4. Nested symlink chain escaping the workspace is rejected.
	nested := makeWorkspace(t)
	if err := os.Symlink("../../outside/evil", filepath.Join(nested, "hop")); err != nil {
		t.Fatalf("failed to create nested symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(nested, "hop"), filepath.Join(nested, "gradlew")); err != nil {
		t.Fatalf("failed to create nested wrapper: %v", err)
	}
	if _, err := p.ResolveExecutable("./gradlew", nested); err == nil {
		t.Fatalf("expected nested symlink escape to be rejected")
	}

	// 5. Dangling symlink is rejected (fail closed).
	dangling := makeWorkspace(t)
	if err := os.Symlink(filepath.Join(dangling, "missing"), filepath.Join(dangling, "gradlew")); err != nil {
		t.Fatalf("failed to create dangling symlink: %v", err)
	}
	if _, err := p.ResolveExecutable("./gradlew", dangling); err == nil {
		t.Fatalf("expected dangling symlink to be rejected")
	}

	// 6. Directory target is rejected.
	directory := makeWorkspace(t)
	if err := os.MkdirAll(filepath.Join(directory, "subdir"), 0o700); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(directory, "subdir"), filepath.Join(directory, "gradlew")); err != nil {
		t.Fatalf("failed to create directory symlink: %v", err)
	}
	if _, err := p.ResolveExecutable("./gradlew", directory); err == nil {
		t.Fatalf("expected directory symlink to be rejected")
	}
}

// TestResolveExecutable_SymlinkedWorkspaceRoot ensures a workspace root that is itself reached via a
// symlink is canonicalized before containment is evaluated.
func TestResolveExecutable_SymlinkedWorkspaceRoot(t *testing.T) {
	p := NewDefaultCommandPolicy()

	root := t.TempDir()
	realWorkspace := filepath.Join(root, "real")
	if err := os.MkdirAll(realWorkspace, 0o700); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realWorkspace, "gradlew"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("failed to write executable: %v", err)
	}
	linkedRoot := filepath.Join(root, "linked")
	if err := os.Symlink(realWorkspace, linkedRoot); err != nil {
		t.Skipf("symlink creation unavailable on %s: %v", runtime.GOOS, err)
	}

	resolved, err := p.ResolveExecutable("./gradlew", linkedRoot)
	if err != nil {
		t.Fatalf("expected executable under symlinked root to resolve, got: %v", err)
	}
	if !isWithin(mustEval(t, realWorkspace), resolved) {
		t.Fatalf("expected canonical path inside the real workspace, got: %s", resolved)
	}
}

// TestResolveExecutable_ReportsPolicyErrorType ensures containment failures map to the same error
// sentinel the HTTP layer translates into 403, so behaviour stays consistent across entry points.
func TestResolveExecutable_ReportsPolicyErrorType(t *testing.T) {
	p := NewDefaultCommandPolicy()
	workspace := t.TempDir()

	if _, err := p.ResolveExecutable("./not-there", workspace); err == nil {
		t.Fatalf("expected missing executable to be rejected")
	} else if !isPolicyError(err) {
		t.Fatalf("expected ErrCommandNotAllowed, got %v", err)
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("failed to evaluate %s: %v", path, err)
	}
	return resolved
}

func isPolicyError(err error) bool {
	return err != nil && (errors.Is(err, domain.ErrCommandNotAllowed))
}
