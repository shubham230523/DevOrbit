package security

import (
	"testing"
)

func TestCommandPolicy_Validation(t *testing.T) {
	policy := NewDefaultCommandPolicy()

	// Authorized binaries
	if err := policy.ValidateCommand("git", []string{"status"}); err != nil {
		t.Errorf("Expected 'git status' to be authorized, got: %v", err)
	}

	if err := policy.ValidateCommand("gradlew", []string{"build"}); err != nil {
		t.Errorf("Expected 'gradlew build' to be authorized, got: %v", err)
	}

	// Unauthorized binary
	if err := policy.ValidateCommand("powershell", []string{"-Command", "Get-Process"}); err == nil {
		t.Errorf("Expected unauthorized binary 'powershell' to be rejected")
	}

	// Command injection attempt via arguments
	if err := policy.ValidateCommand("git", []string{"status", "; rm -rf /"}); err == nil {
		t.Errorf("Expected shell injection character in argument to be rejected")
	}
}
