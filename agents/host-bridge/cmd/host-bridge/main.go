package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"devorbit.net/agents/host-bridge/internal/application"
	"devorbit.net/agents/host-bridge/internal/config"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
	"devorbit.net/agents/host-bridge/internal/infrastructure/filesystem"
	"devorbit.net/agents/host-bridge/internal/infrastructure/git"
	"devorbit.net/agents/host-bridge/internal/infrastructure/process"
	"devorbit.net/agents/host-bridge/internal/security"
	transport "devorbit.net/agents/host-bridge/internal/transport/http"
)

func main() {
	cfg := config.LoadConfig()

	// 1. Security Layer
	jail := security.NewWorkspaceJail(cfg.AllowedRoots)
	policy := security.NewDefaultCommandPolicy()

	// 2. Infrastructure Layer
	storage := filesystem.NewStorageEngine(jail, cfg.MaxFileSizeMB*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	gitClient := git.NewGitClient(jail)
	adbClient := adb.NewADBClient("adb")

	// 3. Application Layer
	wsSvc := application.NewWorkspaceService(jail)
	fsSvc := application.NewFilesystemService(storage)
	psSvc := application.NewProcessService(procRunner)
	gsSvc := application.NewGitService(gitClient)
	dsSvc := application.NewDeviceService(adbClient)

	// 4. Transport Layer
	serverEngine := transport.NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	mux := http.NewServeMux()
	serverEngine.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("DevOrbit Host Bridge v1.1.0 listening on %s (Allowed Roots: %d)", addr, len(cfg.AllowedRoots))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Host Bridge server failed: %v", err)
		}
	}()

	// Graceful Shutdown Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down Host Bridge gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Host Bridge forced to shutdown: %v", err)
	}

	log.Println("Host Bridge exited cleanly")
}
