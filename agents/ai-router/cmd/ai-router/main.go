package main

import (
	"log"

	"devorbit.net/agents/ai-router/pkg/router"
)

func main() {
	r := router.NewAIRouter()
	log.Printf("AI Router initialized with %d models", len(r.ListAvailableModels()))
}
