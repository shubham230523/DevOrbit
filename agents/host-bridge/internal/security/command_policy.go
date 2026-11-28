package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"devorbit.net/agents/host-bridge/internal/domain"
)

// Centralized command execution policy.
//
// Every command execution boundary in the Host Bridge (direct HTTP process execution and the
// agent's execute_command tool) routes through this policy, so authorization cannot be bypassed by
// choosing a different entry point. The policy is intentionally argument-aware: an executable
// allowlist alone is insufficient because `git`, `gradle` and `adb` can each launch arbitrary code
// through a handful of options.
const (
	// MaxCommandArgs bounds argument count to prevent resource exhaustion and pathological argv.
	MaxCommandArgs = 64
	// MaxCommandArgLength bounds a single argument (paths, messages, pathspecs).
	MaxCommandArgLength = 4096
)

// commandFamily describes the permitted shape of one allowlisted executable family.
type commandFamily struct {
	name string
	// subcommands is the allowlist of accepted sub-commands/tasks (lower-case).
	subcommands map[string]bool
	// allowAnySubcommand is used for Gradle, whose task names are project-defined.
	allowAnySubcommand bool
	// requireSubcommand rejects invocations that supply no sub-command (e.g. bare `git`).
	requireSubcommand bool
	// allowedFlagOnly permits flag-only invocations such as `git --version` or `gradlew --version`.
	allowedFlagOnly map[string]bool
	// deniedShortFlags are rejected anywhere inside a short-option cluster, so `-c`, `-ckey=value`
	// and clustered forms such as `-cf` are all rejected. Keys are single characters and matching is
	// case-sensitive (`-i` and `-I` are different options).
	deniedShortFlags map[byte]bool
	// deniedLongFlags are rejected by option NAME, so `--init-script`, `--init-script=x` and
	// `--init-script x` are all covered by one entry.
	deniedLongFlags map[string]bool
	// valueShortFlags consume the remainder of the token (or the next argument) as their value, so
	// scanning stops there: `-m "fix -c handling"` must not inspect the message for option letters.
	valueShortFlags map[byte]bool
	// valueFlags are options that consume the following argument (validated, not treated as a
	// sub-command), e.g. `adb -s <serial> devices`.
	valueFlags map[string]bool
	// validateValue validates the value consumed by valueFlags, when present.
	validateValue func(flag, value string) error
}

// CommandPolicy validates executable identity, arguments, working directory and environment for
// every supported command family. It is the single source of truth for command authorization.
type CommandPolicy struct {
	AllowedBinaries map[string]bool
	families        map[string]*commandFamily
	MaxArgs         int
	MaxArgLength    int
}

// forbiddenArgumentCharacters are rejected inside arguments. They are shell metacharacters and
// control characters: even though DevOrbit never invokes a shell, these values indicate injection
// attempts and must not reach an allowlisted binary that may interpret them.
const forbiddenArgumentCharacters = ";&|`$<>\x00\n\r"

// NewDefaultCommandPolicy builds the policy used by the Host Bridge in production.
func NewDefaultCommandPolicy() *CommandPolicy {
	git := &commandFamily{
		name: "git",
		subcommands: map[string]bool{
			// Inspection
			"status": true, "diff": true, "log": true, "show": true, "branch": true,
			"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true,
			"blame": true, "describe": true, "shortlog": true, "grep": true,
			"version": true, "remote": true, "tag": true,
			// Working-tree mutation (approval-gated where routed through the agent)
			"add": true, "restore": true, "checkout": true, "switch": true, "stash": true,
			"commit": true, "reset": true, "clean": true, "apply": true, "rm": true, "mv": true,
			// Synchronization
			"fetch": true, "pull": true, "push": true, "merge": true,
		},
		requireSubcommand: true,
		allowedFlagOnly:   map[string]bool{"--version": true, "-v": true},
		// `-c` and `-C` take a value, which is why the attached form `-cfoo=bar` is valid Git syntax
		// and must be rejected by cluster scanning rather than exact-token matching.
		deniedShortFlags: map[byte]bool{'c': true, 'C': true},
		valueShortFlags:  map[byte]bool{'c': true, 'C': true, 'm': true, 'n': true, 'F': true, 'o': true},
		deniedLongFlags: map[string]bool{
			// Configuration injection (aliases, hooks, pager, sshCommand) and repository relocation.
			"config-env": true, "git-dir": true, "work-tree": true, "namespace": true,
			// Program execution surfaces.
			"exec": true, "exec-path": true, "upload-pack": true, "receive-pack": true,
			"open-files-in-pager": true,
			// External diff/textconv drivers execute configured programs.
			"ext-diff": true, "textconv": true,
			// Reads or writes paths outside the workspace.
			"no-index": true, "output": true,
		},
	}

	gradle := &commandFamily{
		name:               "gradle",
		allowAnySubcommand: true, // task names are defined by the project
		// Case-sensitive: `-i` (info) stays legal while `-I` (init script) is rejected.
		deniedShortFlags: map[byte]bool{'I': true, 'b': true, 'c': true, 'p': true, 'g': true},
		valueShortFlags:  map[byte]bool{'I': true, 'b': true, 'c': true, 'p': true, 'g': true, 'P': true, 'D': true},
		deniedLongFlags: map[string]bool{
			// Script/build-file overrides execute arbitrary Groovy/Kotlin outside the build.
			"init-script": true, "build-file": true, "settings-file": true,
			// Relocate the project, included builds or the user home away from the jailed workspace.
			"project-dir": true, "include-build": true, "gradle-user-home": true,
			"project-cache-dir": true,
		},
	}

	adb := &commandFamily{
		name: "adb",
		subcommands: map[string]bool{
			// DevOrbit only inspects connected devices; it never drives a device shell.
			"devices": true, "version": true, "get-state": true,
			"wait-for-device": true, "start-server": true, "kill-server": true,
		},
		requireSubcommand: true,
		valueShortFlags:  map[byte]bool{'s': true, 'P': true, 'H': true},
		valueFlags:       map[string]bool{"-s": true, "-P": true, "-H": true},
		validateValue: func(flag, value string) error {
			if value == "" || len(value) > 128 {
				return fmt.Errorf("%w: invalid value for %s", domain.ErrCommandNotAllowed, flag)
			}
			for _, r := range value {
				isSafe := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
					(r >= '0' && r <= '9') || r == '.' || r == ':' || r == '-' || r == '_'
				if !isSafe {
					return fmt.Errorf("%w: invalid value for %s", domain.ErrCommandNotAllowed, flag)
				}
			}
			return nil
		},
	}

	families := map[string]*commandFamily{
		"git":    git,
		"gradle": gradle,
		"adb":    adb,
	}

	return &CommandPolicy{
		AllowedBinaries: map[string]bool{
			"git": true, "git.exe": true,
			"gradlew": true, "gradlew.bat": true, "gradle": true, "gradle.bat": true, "gradle.exe": true,
			"adb": true, "adb.exe": true,
		},
		families:     families,
		MaxArgs:      MaxCommandArgs,
		MaxArgLength: MaxCommandArgLength,
	}
}

// canonicalFamily maps an executable base name to its policy family.
func canonicalFamily(baseName string) string {
	base := strings.ToLower(filepath.Base(baseName))
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".bat")
	switch base {
	case "git":
		return "git"
	case "gradle", "gradlew":
		return "gradle"
	case "adb":
		return "adb"
	}
	return ""
}

// ValidateCommand validates the executable identity and every argument for the command.
// It never echoes full argument values in errors, so rejected prompts cannot leak user content
// or secrets into logs or API responses.
func (p *CommandPolicy) ValidateCommand(command string, args []string) error {
	if p == nil {
		return fmt.Errorf("%w: no command policy configured", domain.ErrCommandNotAllowed)
	}
	if err := p.validateCommandName(command); err != nil {
		return err
	}

	maxArgs := p.MaxArgs
	if maxArgs <= 0 {
		maxArgs = MaxCommandArgs
	}
	maxArgLength := p.MaxArgLength
	if maxArgLength <= 0 {
		maxArgLength = MaxCommandArgLength
	}
	if len(args) > maxArgs {
		return fmt.Errorf("%w: too many arguments (%d > %d)", domain.ErrCommandNotAllowed, len(args), maxArgs)
	}

	for i, arg := range args {
		if len(arg) > maxArgLength {
			return fmt.Errorf("%w: argument %d exceeds the maximum length of %d", domain.ErrCommandNotAllowed, i, maxArgLength)
		}
		if !utf8.ValidString(arg) {
			return fmt.Errorf("%w: argument %d is not valid UTF-8", domain.ErrCommandNotAllowed, i)
		}
		if strings.ContainsAny(arg, forbiddenArgumentCharacters) {
			return fmt.Errorf("%w: argument %d contains forbidden shell or control characters", domain.ErrCommandNotAllowed, i)
		}
	}

	family := p.families[canonicalFamily(command)]
	if family == nil {
		return fmt.Errorf("%w: binary '%s' is not authorized", domain.ErrCommandNotAllowed, filepath.Base(command))
	}
	return family.validate(args)
}

// validateCommandName enforces executable identity: a bare allowlisted name, or a workspace-relative
// invocation such as ./gradlew. Absolute paths, drive letters and traversal segments are rejected so
// a caller cannot execute an arbitrary binary that merely shares an allowlisted base name.
func (p *CommandPolicy) validateCommandName(command string) error {
	name := strings.TrimSpace(command)
	if name == "" {
		return fmt.Errorf("%w: command is required", domain.ErrCommandNotAllowed)
	}
	if strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("%w: command contains control characters", domain.ErrCommandNotAllowed)
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\\`) {
		return fmt.Errorf("%w: absolute executable paths are not permitted", domain.ErrCommandNotAllowed)
	}
	if len(name) >= 2 && name[1] == ':' {
		return fmt.Errorf("%w: absolute executable paths are not permitted", domain.ErrCommandNotAllowed)
	}
	if strings.ContainsAny(name, `/\`) {
		// Only "./tool" / ".\tool" style workspace-relative invocation of the wrapper itself is
		// supported. Sub-directory paths are rejected because a repository could ship an executable
		// that merely shares an allowlisted base name (e.g. bin/git).
		if !strings.HasPrefix(name, "./") && !strings.HasPrefix(name, `.\`) {
			return fmt.Errorf("%w: executable must be a bare name or a workspace-relative path", domain.ErrCommandNotAllowed)
		}
		if strings.Contains(name, "..") {
			return fmt.Errorf("%w: executable path must not contain traversal segments", domain.ErrCommandNotAllowed)
		}
		reduced := strings.TrimPrefix(strings.TrimPrefix(name, "./"), `.\`)
		if strings.ContainsAny(reduced, `/\`) {
			return fmt.Errorf("%w: executable must be the workspace-root wrapper itself", domain.ErrCommandNotAllowed)
		}
	}
	if !p.AllowedBinaries[strings.ToLower(filepath.Base(name))] {
		return fmt.Errorf("%w: binary '%s' is not authorized", domain.ErrCommandNotAllowed, filepath.Base(name))
	}
	return nil
}

// ResolveExecutable returns the executable path to run for command inside workDir.
//
// Containment is enforced on the REAL filesystem, not lexically. A workspace-relative wrapper is
// accepted only when all of the following hold:
//   - the lexical path stays inside the workspace root;
//   - the workspace root itself is canonicalized (symlinked roots are resolved first);
//   - the target exists as a regular file (dangling symlinks and directories are rejected);
//   - after resolving every symlink, the real path still lives inside the canonicalized workspace,
//     so a wrapper cannot redirect execution to a binary outside the jail (including nested links).
//
// Residual risk: this is a check-then-execute sequence, so a writable workspace can still swap the
// file between validation and process start (TOCTOU). DevOrbit executes the resolved absolute path
// and does not re-resolve links, which narrows but does not eliminate that window. Full elimination
// requires OS-level primitives (openat/O_NOFOLLOW), which are platform-specific and out of scope.
func (p *CommandPolicy) ResolveExecutable(command, workDir string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("%w: no command policy configured", domain.ErrCommandNotAllowed)
	}
	if err := p.validateCommandName(command); err != nil {
		return "", err
	}
	name := strings.TrimSpace(command)
	if !strings.ContainsAny(name, `/\`) {
		// Bare allowlisted names are resolved through PATH by the OS; nothing to canonicalize.
		return filepath.Base(name), nil
	}

	resolved := filepath.Clean(filepath.Join(workDir, name))
	if !isWithin(workDir, resolved) {
		return "", fmt.Errorf("%w: executable path escapes the workspace", domain.ErrCommandNotAllowed)
	}

	realRoot, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return "", fmt.Errorf("%w: workspace root cannot be resolved", domain.ErrCommandNotAllowed)
	}
	realExecutable, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		// Dangling symlink, missing file, or unresolvable path: fail closed.
		return "", fmt.Errorf("%w: executable cannot be resolved inside the workspace", domain.ErrCommandNotAllowed)
	}
	if !isWithin(realRoot, realExecutable) {
		return "", fmt.Errorf("%w: executable resolves outside the workspace", domain.ErrCommandNotAllowed)
	}

	info, err := os.Lstat(realExecutable)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: executable is not a regular file", domain.ErrCommandNotAllowed)
	}

	return realExecutable, nil
}

// isWithin reports whether target is root itself or a descendant of root.
func isWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func (f *commandFamily) validate(args []string) error {
	seenSubcommand := false
	flagOnly := true

	for i := 0; i < len(args); i++ {
		arg := args[i]
		lower := strings.ToLower(arg)

		if err := f.validateFlag(arg); err != nil {
			return err
		}

		if f.valueFlags[arg] {
			if i+1 >= len(args) {
				return fmt.Errorf("%w: option '%s' requires a value", domain.ErrCommandNotAllowed, arg)
			}
			next := args[i+1]
			if f.validateValue != nil {
				if err := f.validateValue(arg, next); err != nil {
					return err
				}
			}
			i++
			continue
		}

		if strings.HasPrefix(arg, "-") {
			continue
		}

		flagOnly = false
		if seenSubcommand {
			// Additional positional arguments are permitted (paths, messages, pathspecs).
			continue
		}
		seenSubcommand = true

		if f.allowAnySubcommand {
			continue
		}
		if !f.subcommands[lower] {
			return fmt.Errorf("%w: '%s' is not a permitted %s sub-command", domain.ErrCommandNotAllowed, lower, f.name)
		}
	}

	if !seenSubcommand {
		if flagOnly && f.allowedFlagOnly != nil {
			for _, arg := range args {
				if !f.allowedFlagOnly[strings.ToLower(arg)] {
					return fmt.Errorf("%w: %s requires a permitted sub-command", domain.ErrCommandNotAllowed, f.name)
				}
			}
			return nil
		}
		if f.requireSubcommand {
			return fmt.Errorf("%w: %s requires a permitted sub-command", domain.ErrCommandNotAllowed, f.name)
		}
	}
	return nil
}

// validateFlag rejects prohibited option forms. It parses arguments structurally instead of matching
// whole tokens, so every equivalent spelling of a dangerous option is covered:
//
//	--init-script / --init-script=x   -> long option name lookup
//	-I script.gradle / -Iscript.gradle -> short-option cluster scan
//	-cf / -Ic                          -> cluster scan reaches the denied letter
//
// Matching is case-sensitive because option semantics are: Gradle `-i` (info) differs from `-I`
// (init script), and Git `-c` (config) differs from `-C` (directory). When a cluster contains an
// option that consumes a value, scanning stops at that point so option-like text inside a value
// (for example a commit message containing "-c") is not misinterpreted.
func (f *commandFamily) validateFlag(arg string) error {
	if arg == "--" || len(arg) < 2 || arg[0] != '-' {
		return nil
	}

	if strings.HasPrefix(arg, "--") {
		name := arg[2:]
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			name = name[:idx]
		}
		if f.deniedLongFlags[name] {
			return fmt.Errorf("%w: option '--%s' is not permitted for %s", domain.ErrCommandNotAllowed, name, f.name)
		}
		return nil
	}

	for i := 1; i < len(arg); i++ {
		c := arg[i]
		if f.deniedShortFlags[c] {
			return fmt.Errorf("%w: option '-%c' is not permitted for %s", domain.ErrCommandNotAllowed, c, f.name)
		}
		if f.valueShortFlags[c] {
			// The remainder of this token (or the next argument) is the option value.
			return nil
		}
	}
	return nil
}

// RequiresApproval reports whether executing the command needs an explicit, auditable user
// authorization decision in addition to session permissions.
//
// Gradle always evaluates project-defined build scripts, so any task — including "help" or
// "tasks" — can run arbitrary code supplied by the repository. Argument validation cannot make that
// safe, therefore Gradle execution is approval-gated. Git and adb are inspection-scoped by the
// argument policy and do not require approval.
func (p *CommandPolicy) RequiresApproval(command string, args []string) (bool, string) {
	if p == nil {
		return true, "no command policy configured"
	}
	switch canonicalFamily(command) {
	case "gradle":
		return true, "gradle executes project-defined build logic"
	default:
		return false, ""
	}
}

// deniedEnvironmentExact lists environment variables that redirect the dynamic loader, Git
// repository discovery, or JVM option injection. They are removed before spawning allowlisted tools
// so a caller cannot execute code or escape the workspace through the inherited environment.
var deniedEnvironmentExact = map[string]bool{
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true, "DYLD_FRAMEWORK_PATH": true,
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true,
	"GIT_CONFIG": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "GIT_CONFIG_COUNT": true,
	// External diff/textconv/pager/editor programs are all execution surfaces.
	"GIT_EXTERNAL_DIFF": true, "GIT_PAGER": true, "PAGER": true, "GIT_EDITOR": true,
	"GIT_SEQUENCE_EDITOR": true, "GIT_TEMPLATE_DIR": true,
	"BASH_ENV": true, "ENV": true, "PS4": true, "PROMPT_COMMAND": true,
	"JAVA_TOOL_OPTIONS": true, "_JAVA_OPTIONS": true, "JDK_JAVA_OPTIONS": true,
	"GRADLE_OPTS": true, "NODE_OPTIONS": true, "PYTHONSTARTUP": true, "PERL5OPT": true,
}

// deniedEnvironmentPrefixes are removed by prefix (e.g. GIT_CONFIG_KEY_0).
var deniedEnvironmentPrefixes = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}

// SanitizeEnvironment removes dangerous variables from an inherited environment before executing an
// allowlisted command. Safe values are preserved so legitimate developer tooling keeps working.
func SanitizeEnvironment(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	sanitized := make([]string, 0, len(env))
	for _, entry := range env {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		upperKey := strings.ToUpper(key)
		if deniedEnvironmentExact[upperKey] {
			continue
		}
		denied := false
		for _, prefix := range deniedEnvironmentPrefixes {
			if strings.HasPrefix(upperKey, prefix) {
				denied = true
				break
			}
		}
		if denied {
			continue
		}
		sanitized = append(sanitized, entry)
	}
	return sanitized
}
