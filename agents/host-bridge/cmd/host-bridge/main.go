package main

import (
	"log"

	"devorbit.net/agents/host-bridge/internal/discovery"
	"devorbit.net/agents/host-bridge/internal/sanitizer"
)

func main() {
	s := sanitizer.NewPathSanitizer([]string{"/home/user/projects", "C:\\Users\\shubham\\Documents\\PersonalProjects\\DevOrbit"})
	d := discovery.NewWorkspaceScanner()

	log.Printf("Host Bridge initialized with %d allowed root(s) and scanner ready", len(s.AllowedRoots))
	_ = d
}
