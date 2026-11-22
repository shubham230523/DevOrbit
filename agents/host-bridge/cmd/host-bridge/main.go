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
	"devorbit.net/agents/host-bridge/internal/transport"
	httptransport "devorbit.net/agents/host-bridge/internal/transport/http"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Host Bridge configuration error: %v", err)
	}

	// 1. Security Layer
	jail := security.NewWorkspaceJail(cfg.AllowedRoots)
	policy := security.NewDefaultCommandPolicy()
	pairingEngine := security.NewPairingEngine(cfg.HostID, cfg.HostName, cfg.HostSecret)
	rateLimiter := security.NewRateLimiter(5, 10*time.Minute)
	idempotencyMgr := security.NewIdempotencyManager(30 * time.Minute)

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
	serverEngine := httptransport.NewServer(wsSvc, fsSvc, psSvc, gsSvc, dsSvc)
	serverEngine.SetPairingEngine(pairingEngine)
	serverEngine.SetRateLimiter(rateLimiter)
	serverEngine.SetIdempotencyManager(idempotencyMgr)

	if cfg.AdvertisedURL != "" {
		serverEngine.SetAdvertisedURL(cfg.AdvertisedURL)
	}
	serverEngine.SetBrokerEndpoints(cfg.BrokerHTTPURL, cfg.BrokerWSURL, cfg.HostSecret)
	serverEngine.SetTrustedProxies(cfg.TrustedProxies)
	serverEngine.SetIsProduction(cfg.Environment == "production")

	brokerAdminSecret := cfg.BrokerSecret
	if brokerAdminSecret == "" && cfg.Environment != "production" {
		brokerAdminSecret = cfg.HostSecret
	}
	brokerAuthorizer := transport.NewHTTPBrokerAuthorizer(cfg.BrokerHTTPURL, brokerAdminSecret, nil)
	serverEngine.SetBrokerAuthorizer(brokerAuthorizer)

	mux := http.NewServeMux()
	serverEngine.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 5. Outbound Connection Broker Client
	brokerClient := transport.NewBrokerClient(cfg.BrokerWSURL, cfg.HostID, "sess-host-init-001", cfg.HostSecret, serverEngine)
	e2eSess, _ := security.NewE2ESession("sess-host-init-001", "phone-android-01", cfg.HostSecret, 24*time.Hour)
	brokerClient.SetE2ESession(e2eSess)

	brokerClient.Start(context.Background())
	defer brokerClient.Stop()

	go func() {
		log.Printf("DevOrbit Host Bridge v1.2.1 listening on %s (HostID: %s, Allowed Roots: %d)", addr, cfg.HostID, len(cfg.AllowedRoots))
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
