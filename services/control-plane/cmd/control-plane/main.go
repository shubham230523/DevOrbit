package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

func main() {
	authService := auth.NewService("devorbit-secret-key")
	pairingService := pairing.NewService("devorbit-pairing-secret-key")
	machineStore := NewMachineStore()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"control-plane","version":"1.2.0"}`)
	})

	mux.HandleFunc("/api/v1/auth/pair", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token, err := authService.GenerateToken("host-123", "device-456")
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
		ch, err := pairingService.CreateChallenge("host-windows-01", "Desktop Workstation", 5*time.Minute)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ch)
	})

	mux.HandleFunc("/api/v1/machines", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var m Machine
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
				http.Error(w, "Invalid machine payload", http.StatusBadRequest)
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

	log.Println("DevOrbit Control Plane listening on :8080...")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("Control Plane server failed: %v", err)
	}
}
