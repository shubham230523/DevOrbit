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
	// deniedFlags are rejected verbatim (lower-case).
	deniedFlags map[string]bool
	// deniedFlagPrefixes are rejected when an argument starts with them (lower-case).
	deniedFlagPrefixes []string
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
		deniedFlags: map[string]bool{
			// -c / --config-env inject arbitrary configuration (including aliases and hooks).
			"-c": true, "--config-env": true,
			// Redirect repository discovery outside the jailed workspace.
			"-C": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
			// Execute or expose arbitrary programs.
			"--exec": true, "--upload-pack": true, "--receive-pack": true,
			// Pager selection can execute an arbitrary program (`git log -p` patch output is fine).
			"--open-files-in-pager": true,
			"--exec-path": true,
		},
		deniedFlagPrefixes: []string{
			"--git-dir=", "--work-tree=", "--config-env=", "--upload-pack=",
			"--receive-pack=", "--open-files-in-pager=", "--exec-path=", "--exec=",
		},
	}

	gradle := &commandFamily{
		name:               "gradle",
		allowAnySubcommand: true, // task names are defined by the project
		deniedFlags: map[string]bool{
			// Script/build-file overrides execute arbitrary Groovy/Kotlin outside the build.
			"-I": true, "--init-script": true, "-b": true, "--build-file": true,
			"-c": true, "--settings-file": true,
			// Relocate the project or included builds away from the jailed workspace.
			"-p": true, "--project-dir": true, "--include-build": true,
			"--gradle-user-home": true, "-g": true,
			"--project-cache-dir": true,
		},
		deniedFlagPrefixes: []string{
			"--init-script=", "--build-file=", "--settings-file=", "--project-dir=",
			"--include-build=", "--gradle-user-home=", "--project-cache-dir=",
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
		valueFlags:        map[string]bool{"-s": true, "-P": true, "-H": true},
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

// ResolveExecutable returns the executable path to run for command inside workDir, rejecting any
// workspace-relative path that escapes the workspace root.
func (p *CommandPolicy) ResolveExecutable(command, workDir string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("%w: no command policy configured", domain.ErrCommandNotAllowed)
	}
	if err := p.validateCommandName(command); err != nil {
		return "", err
	}
	name := strings.TrimSpace(command)
	if !strings.ContainsAny(name, `/\`) {
		return filepath.Base(name), nil
	}
	resolved := filepath.Clean(filepath.Join(workDir, name))
	rel, err := filepath.Rel(workDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: executable path escapes the workspace", domain.ErrCommandNotAllowed)
	}
	return resolved, nil
}

func (f *commandFamily) validate(args []string) error {
	seenSubcommand := false
	flagOnly := true

	for i := 0; i < len(args); i++ {
		arg := args[i]
		lower := strings.ToLower(arg)

		// Exact option matching is case-sensitive: for Gradle `-i` (info) is harmless while `-I`
		// (init script) executes arbitrary code.
		if f.deniedFlags[arg] {
			return fmt.Errorf("%w: option '%s' is not permitted for %s", domain.ErrCommandNotAllowed, arg, f.name)
		}
		for _, prefix := range f.deniedFlagPrefixes {
			if strings.HasPrefix(lower, prefix) {
				return fmt.Errorf("%w: option prefix '%s' is not permitted for %s", domain.ErrCommandNotAllowed, strings.TrimSuffix(prefix, "="), f.name)
			}
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
