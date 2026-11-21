package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"devorbit.net/services/control-plane/internal/auth"
	"devorbit.net/services/control-plane/internal/pairing"
)

type Machine struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OS        string    `json:"os"`
	Address   string    `json:"address"`
	LastSeen  time.Time `json:"last_seen"`
	IsOnline  bool      `json:"is_online"`
}

type MachineStore struct {
	mu       sync.RWMutex
	machines map[string]*Machine
}

func NewMachineStore() *MachineStore {
	return &MachineStore{machines: make(map[string]*Machine)}
}

func (s *MachineStore) Register(m *Machine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m.LastSeen = time.Now()
	m.IsOnline = true
	s.machines[m.ID] = m
}

func (s *MachineStore) List() []*Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Machine, 0, len(s.machines))
	for _, m := range s.machines {
		list = append(list, m)
	}
	return list
}

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	env := strings.ToLower(getEnvOrDefault("DEVORBIT_ENV", "development"))
	jwtSecret := os.Getenv("DEVORBIT_JWT_SECRET")
	pairingSecret := os.Getenv("DEVORBIT_PAIRING_SECRET")

	if env == "production" {
		if jwtSecret == "" || len(jwtSecret) < 16 {
			log.Fatalf("Control Plane Configuration Error: DEVORBIT_JWT_SECRET is required and must be at least 16 characters in production mode")
		}
		if pairingSecret == "" || len(pairingSecret) < 16 {
			log.Fatalf("Control Plane Configuration Error: DEVORBIT_PAIRING_SECRET is required and must be at least 16 characters in production mode")
		}
	} else {
		if jwtSecret == "" {
			jwtSecret = "devorbit-control-plane-jwt-secret-key-32b"
		}
		if pairingSecret == "" {
			pairingSecret = "devorbit-control-plane-pairing-secret-key"
		}
	}

	authService := auth.NewService(jwtSecret)
	pairingService := pairing.NewService(pairingSecret)
	machineStore := NewMachineStore()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"control-plane","version":"1.2.1","env":"%s"}`, env)
	})

	mux.HandleFunc("/api/v1/auth/pair", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			MachineID string `json:"machine_id"`
			DeviceID  string `json:"device_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MachineID == "" || req.DeviceID == "" {
			http.Error(w, "invalid request: machine_id and device_id are required", http.StatusBadRequest)
			return
		}

		token, err := authService.GenerateToken(req.MachineID, req.DeviceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"session_token":"%s"}`, token)
	})

	mux.HandleFunc("/api/v1/pairing/challenge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			MachineID string `json:"machine_id"`
			HostName  string `json:"host_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MachineID == "" {
			http.Error(w, "invalid request: machine_id is required", http.StatusBadRequest)
			return
		}

		ch, err := pairingService.CreateChallenge(req.MachineID, req.HostName, 5*time.Minute)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"v":               "1.0",
			"challenge_id":    ch.ID,
			"host_id":         ch.MachineID,
			"host_name":       ch.HostName,
			"pairing_payload": ch.Payload,
			"pairing_secret":  ch.Secret,
			"confirm_url":     "/api/v1/pairing/confirm",
			"broker_url":      getEnvOrDefault("DEVORBIT_BROKER_URL", "ws://localhost:8081"),
			"expires_at":      ch.ExpiresAt.Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/api/v1/pairing/confirm", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ChallengeID     string `json:"challenge_id"`
			DeviceID        string `json:"device_id"`
			DeviceName      string `json:"device_name"`
			DevicePublicKey string `json:"device_public_key"`
			ProofSignature  string `json:"proof_signature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChallengeID == "" || req.DeviceID == "" || req.ProofSignature == "" {
			http.Error(w, `{"error":"invalid request: challenge_id, device_id, and proof_signature are required"}`, http.StatusBadRequest)
			return
		}

		dev, err := pairingService.ConfirmPairing(req.ChallengeID, req.DeviceID, req.DeviceName, req.DevicePublicKey, req.ProofSignature)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, pairing.ErrChallengeExpired) || errors.Is(err, pairing.ErrChallengeUsed) || errors.Is(err, pairing.ErrInvalidProof) {
				status = http.StatusBadRequest
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		token, err := authService.GenerateToken(dev.MachineID, dev.DeviceID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sessBytes := make([]byte, 16)
		_, _ = rand.Read(sessBytes)
		sessionID := fmt.Sprintf("sess-%s", hex.EncodeToString(sessBytes))

		secretBytes := make([]byte, 32)
		_, _ = rand.Read(secretBytes)
		sessionSecret := hex.EncodeToString(secretBytes)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":         "success",
			"session_token":  token,
			"session_id":     sessionID,
			"session_secret": sessionSecret,
			"machine_id":     dev.MachineID,
			"device_id":      dev.DeviceID,
			"broker_url":     getEnvOrDefault("DEVORBIT_BROKER_URL", "ws://localhost:8081"),
			"expires_at":     time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/api/v1/devices/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			MachineID string `json:"machine_id"`
			DeviceID  string `json:"device_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MachineID == "" || req.DeviceID == "" {
			http.Error(w, "invalid request: machine_id and device_id are required", http.StatusBadRequest)
			return
		}
		if err := pairingService.RevokeDevice(req.MachineID, req.DeviceID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
	})

	mux.HandleFunc("/api/v1/machines", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var m Machine
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil || m.ID == "" {
				http.Error(w, "Invalid machine payload: id is required", http.StatusBadRequest)
				return
			}
			machineStore.Register(&m)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(machineStore.List())
	})

	port := getEnvOrDefault("DEVORBIT_CONTROL_PLANE_PORT", "8080")
	log.Printf("DevOrbit Control Plane v1.2.1 listening on :%s (Env: %s)...", port, env)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Control Plane server failed: %v", err)
	}
}
