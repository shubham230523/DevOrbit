package main

import (
	"log"

	"devorbit.net/agents/acp-adapter/internal/acp"
)

func main() {
	adapter := acp.NewACPAdapter("Google Antigravity Agent")
	log.Printf("ACP Adapter ready for agent: %s", adapter.AgentName)
}
