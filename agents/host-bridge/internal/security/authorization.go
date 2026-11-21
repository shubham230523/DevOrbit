package security

import (
	"fmt"
	"path/filepath"
	"strings"

	"devorbit.net/agents/host-bridge/internal/domain"
)

type CommandPolicy struct {
	AllowedBinaries map[string]bool
}

func NewDefaultCommandPolicy() *CommandPolicy {
	return &CommandPolicy{
		AllowedBinaries: map[string]bool{
			"git":         true,
			"git.exe":     true,
			"gradlew":     true,
			"gradlew.bat": true,
			"gradle":      true,
			"gradle.bat":  true,
			"adb":         true,
			"adb.exe":     true,
		},
	}
}

func (p *CommandPolicy) ValidateCommand(command string, args []string) error {
	baseName := strings.ToLower(filepath.Base(command))
	if !p.AllowedBinaries[baseName] && !p.AllowedBinaries[command] {
		return fmt.Errorf("%w: binary '%s' is not authorized", domain.ErrCommandNotAllowed, command)
	}

	// Validate argument sanity (prohibit dangerous shell chaining injection chars)
	for _, arg := range args {
		if strings.ContainsAny(arg, ";&|`$<>") {
			return fmt.Errorf("%w: argument '%s' contains forbidden shell characters", domain.ErrCommandNotAllowed, arg)
		}
	}

	return nil
}
