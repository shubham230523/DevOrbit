package acp

import (
	"encoding/json"
	"errors"
)

type ACPMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type ACPAdapter struct {
	AgentName string
}

func NewACPAdapter(agentName string) *ACPAdapter {
	return &ACPAdapter{AgentName: agentName}
}

func (a *ACPAdapter) ParseMessage(raw []byte) (*ACPMessage, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty ACP raw payload")
	}

	var msg ACPMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}

	if msg.JSONRPC != "2.0" {
		return nil, errors.New("unsupported JSON-RPC version")
	}

	return &msg, nil
}
