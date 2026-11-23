package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	airouter "devorbit.net/agents/ai-router/pkg/router"
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

	// AI Router and Gemini Provider
	aiRouter := airouter.NewAIRouter()
	if cfg.GeminiAPIKey != "" {
		geminiProv, err := airouter.NewGeminiProvider(context.Background(), airouter.GeminiConfig{
			APIKey:       cfg.GeminiAPIKey,
			DefaultModel: cfg.GeminiModel,
		})
		if err != nil {
			log.Printf("AI Router Warning: Failed to initialize Gemini provider: %v", err)
		} else {
			aiRouter.RegisterProvider("Gemini API", geminiProv)
			log.Printf("AI Router: Registered real Gemini Provider (default model: %s)", cfg.GeminiModel)
		}
	} else {
		log.Println("AI Router Warning: DEVORBIT_GEMINI_API_KEY is not set. Real Gemini API requests will require configuration.")
	}
	serverEngine.SetAIRouter(aiRouter)

	mux := http.NewServeMux()
	serverEngine.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 5. Outbound Connection Broker Client
	brokerClient := transport.NewBrokerClient(cfg.BrokerWSURL, cfg.HostID, "", cfg.HostSecret, serverEngine)
	serverEngine.SetSessionProvisioner(func(sessionID, deviceID, sessionSecret string, expiry time.Time) error {
		return brokerClient.ProvisionSession(sessionID, deviceID, sessionSecret, expiry)
	})

	brokerClient.Start(context.Background())
	defer brokerClient.Stop()

	// Print initial pairing challenge on startup for mobile pairing
	if ch, err := pairingEngine.CreateChallenge(30 * time.Minute); err == nil {
		hostIP := getLocalIP()
		confirmURL := fmt.Sprintf("http://%s:%d/api/v1/pairing/confirm", hostIP, cfg.Port)
		if cfg.AdvertisedURL != "" {
			confirmURL = fmt.Sprintf("%s/api/v1/pairing/confirm", strings.TrimRight(cfg.AdvertisedURL, "/"))
		}

		qrMap := map[string]interface{}{
			"v":               "1.0",
			"challenge_id":    ch.ID,
			"host_id":         cfg.HostID,
			"host_name":       cfg.HostName,
			"pairing_payload": ch.Payload,
			"pairing_secret":  ch.Secret,
			"confirm_url":     confirmURL,
			"broker_url":      cfg.BrokerWSURL,
			"expires_at":      ch.ExpiresAt.UTC().Format(time.RFC3339),
		}
		qrBytes, _ := json.MarshalIndent(qrMap, "", "  ")

		fmt.Println("================================================================================")
		fmt.Println("                DEVORBIT HOST BRIDGE — PAIRING CHALLENGE PAYLOAD                ")
		fmt.Println("================================================================================")
		fmt.Println("Copy or scan this JSON payload into DevOrbit Mobile app (Machines -> Pair Host):")
		fmt.Println()
		fmt.Println(string(qrBytes))
		fmt.Println("================================================================================")
	}

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

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

