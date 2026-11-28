package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"

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
	"devorbit.net/services/connection-broker/pkg/observability"
)

func main() {
	// Ensure the Windows console uses UTF-8 so QR block characters render correctly.
	setWindowsUTF8Console()

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Host Bridge configuration error: %v", err)
	}

	// 0. Observability baseline: structured JSON logs (with redaction) and a metrics registry.
	// These are optional plumbing: if a write fails, security operations still proceed.
	logger := observability.NewLogger(os.Stderr, "host-bridge", "1.2.1", cfg.Environment, "info")
	metrics := observability.NewMetrics()
	startedAt := time.Now()
	metrics.SetGaugeFunc("devorbit_uptime_seconds", func() float64 {
		return time.Since(startedAt).Seconds()
	})

	// 1. Security Layer
	jail := security.NewWorkspaceJail(cfg.AllowedRoots)
	policy := security.NewDefaultCommandPolicy()
	pairingEngine := security.NewPairingEngine(cfg.HostID, cfg.HostName, cfg.HostSecret)
	rateLimiter := security.NewRateLimiter(5, 10*time.Minute)
	idempotencyMgr := security.NewIdempotencyManager(30 * time.Minute)

	// 2. Infrastructure Layer
	storage := filesystem.NewStorageEngine(jail, cfg.MaxFileSizeMB*1024*1024)
	procRunner := process.NewProcessRunner(policy, jail)
	procRunner.SetObservability(logger, metrics)
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
	serverEngine.SetObservability(logger, metrics)

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

	// Initialize Gemini Agent Engine, Tool Executor, and Approval Manager
	approvalMgr := application.NewApprovalManager()
	// Gradle execution requires an explicit, single-use approval bound to the requesting session.
	serverEngine.SetApprovalManager(approvalMgr)
	agentExecutor := application.NewAgentToolExecutor(wsSvc, fsSvc, psSvc, gsSvc, dsSvc, approvalMgr)
	agentEngine := application.NewGeminiAgentEngine(aiRouter, agentExecutor, approvalMgr)
	serverEngine.SetAgentEngine(agentEngine)

	mux := http.NewServeMux()
	serverEngine.RegisterRoutes(mux)

	addr := fmt.Sprintf(":%d", cfg.Port)
	if cfg.BindAddr != "" {
		addr = net.JoinHostPort(cfg.BindAddr, fmt.Sprintf("%d", cfg.Port))
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 5. Outbound Connection Broker Client
	if err := serverEngine.SessionStore().EnablePersistence(""); err != nil {
		if errors.Is(err, security.ErrSecureStoreUnavailable) {
			log.Println("Host Bridge Security Notice: OS secure secret store unavailable; HTTP sessions will remain in-memory only and require re-pairing after restart (revocation tombstones remain persisted).")
		} else {
			log.Printf("Host Bridge Warning: Failed enabling HTTP session persistence: %v", err)
		}
	}
	if err := serverEngine.SessionStore().LoadPersistedSessions(); err != nil {
		log.Printf("Host Bridge Warning: Failed loading persisted HTTP sessions (fail-closed; re-pairing may be required): %v", err)
	}

	brokerClient := transport.NewBrokerClient(cfg.BrokerWSURL, cfg.HostID, "", cfg.HostSecret, serverEngine)
	if err := brokerClient.EnableSessionPersistence(""); err != nil {
		if errors.Is(err, security.ErrSecureStoreUnavailable) {
			log.Println("Host Bridge Security Notice: OS secure secret store unavailable; broker sessions will remain in-memory only and require re-pairing after restart.")
		} else {
			log.Printf("Host Bridge Warning: Failed enabling broker session persistence: %v", err)
		}
	}
	serverEngine.SetSessionProvisioner(func(sessionID, deviceID, sessionSecret string, expiry time.Time) error {
		return brokerClient.ProvisionSession(sessionID, deviceID, sessionSecret, expiry)
	})
	serverEngine.SetSessionRevoker(func(sessionID, deviceID string) error {
		var errs []error
		if sessionID != "" {
			if err := brokerClient.RevokeBrokerSession(sessionID); err != nil {
				errs = append(errs, err)
			}
		}
		if deviceID != "" {
			if err := brokerClient.RevokeBrokerDevice(deviceID); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})

	brokerClient.Start(context.Background())
	defer brokerClient.Stop()

	metrics.SetGaugeFunc("devorbit_active_broker_sessions", func() float64 {
		return float64(brokerClient.ActiveSessionCount())
	})

	// Security initialisation is complete (secret store resolved, persistence loaded, broker client
	// started). Only now may the instance advertise readiness.
	serverEngine.SetReady(true)
	metrics.Inc("devorbit_startup_total")
	logger.OperationalEvent(observability.LevelInfo, httptransport.EventStartup, map[string]any{
		"host_id":       cfg.HostID,
		"environment":   cfg.Environment,
		"allowed_roots": len(cfg.AllowedRoots),
		"tls_enabled":   cfg.TLSCertFile != "" && cfg.TLSKeyFile != "",
		"trusted_proxies": len(cfg.TrustedProxies),
	})

	// Print initial pairing challenge on startup for mobile pairing
	if ch, err := pairingEngine.CreateChallenge(30 * time.Minute); err == nil {
		hasNativeTLS := cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""
		qrMap, err := serverEngine.BuildStartupPairingQRPayload(ch, getLocalIP(), cfg.Port, hasNativeTLS)
		if err != nil {
			log.Fatalf("Host Bridge startup QR error: %v", err)
		}
		if cfg.Environment != "production" && !hasNativeTLS {
			log.Println("DEVELOPMENT MODE ONLY WARNING: Host Bridge HTTP listener is running WITHOUT TLS for local LAN development pairing. Set DEVORBIT_ENV=production with TLS or trusted HTTPS reverse proxy for production.")
		}
		compactJSON, _ := json.Marshal(qrMap)
		qrBytes, _ := json.MarshalIndent(qrMap, "", "  ")

		fmt.Println("================================================================================")
		fmt.Println("                DEVORBIT HOST BRIDGE — PAIRING QR CODE & PAYLOAD                 ")
		fmt.Println("================================================================================")

		if q, err := qrcode.New(string(compactJSON), qrcode.Low); err == nil {
			q.DisableBorder = true
			bitmap := q.Bitmap()
			fmt.Println("Scan this Compact QR Code in DevOrbit Mobile (Machines -> Pair Host):")
			fmt.Println()
			for y := 0; y < len(bitmap); y += 2 {
				fmt.Print("  ")
				for x := 0; x < len(bitmap[y]); x++ {
					top := bitmap[y][x]
					bottom := false
					if y+1 < len(bitmap) {
						bottom = bitmap[y+1][x]
					}
					switch {
					case top && bottom:
						fmt.Print("█")
					case top && !bottom:
						fmt.Print("▀")
					case !top && bottom:
						fmt.Print("▄")
					default:
						fmt.Print(" ")
					}
				}
				fmt.Println()
			}
			fmt.Println()
		}

		fmt.Println("Or copy/paste this JSON payload manually (Machines -> Pair Host):")
		fmt.Println()
		fmt.Println(string(qrBytes))
		fmt.Println("================================================================================")
	}

	go func() {
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			log.Printf("DevOrbit Host Bridge v1.2.1 listening with TLS on %s (HostID: %s, Env: %s, Allowed Roots: %d)", addr, cfg.HostID, cfg.Environment, len(cfg.AllowedRoots))
			if err := srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				log.Fatalf("Host Bridge TLS server failed: %v", err)
			}
			return
		}
		log.Printf("DevOrbit Host Bridge v1.2.1 listening on %s (HostID: %s, Env: %s, Allowed Roots: %d)", addr, cfg.HostID, cfg.Environment, len(cfg.AllowedRoots))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Host Bridge server failed: %v", err)
		}
	}()

	// Graceful Shutdown Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down Host Bridge gracefully...")
	logger.OperationalEvent(observability.LevelInfo, httptransport.EventShutdown, map[string]any{
		"reason": "signal",
	})
	serverEngine.SetReady(false)
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
	var fallbackIP string
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			ipStr := ip.String()
			if strings.HasPrefix(ipStr, "169.254.") {
				continue
			}
			if strings.HasPrefix(ipStr, "10.") || strings.HasPrefix(ipStr, "192.168.") {
				return ipStr
			}
			fallbackIP = ipStr
		}
	}
	if fallbackIP != "" {
		return fallbackIP
	}
	return "127.0.0.1"
}

