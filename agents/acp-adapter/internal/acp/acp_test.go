package acp

import (
	"testing"
)

func TestACPAdapter_ParseValidMessage(t *testing.T) {
	adapter := NewACPAdapter("Google Antigravity Agent")

	raw := []byte(`{"jsonrpc":"2.0","id":"1","method":"session/initialize","params":{"workspace":"/home/user/app"}}`)
	msg, err := adapter.ParseMessage(raw)

	if err != nil {
		t.Fatalf("Expected valid ACP parse, got: %v", err)
	}

	if msg.Method != "session/initialize" {
		t.Errorf("Expected method 'session/initialize', got '%s'", msg.Method)
	}
}

func TestACPAdapter_ParseInvalidJSONRPC(t *testing.T) {
	adapter := NewACPAdapter("Google Antigravity Agent")

	raw := []byte(`{"jsonrpc":"1.0","id":"1","method":"session/initialize"}`)
	_, err := adapter.ParseMessage(raw)

	if err == nil {
		t.Errorf("Expected error for invalid JSON-RPC version 1.0")
	}
}
