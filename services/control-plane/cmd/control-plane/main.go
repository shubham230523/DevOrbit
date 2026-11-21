package main

import (
	"fmt"
	"log"
	"net/http"

	"devorbit.net/services/control-plane/internal/auth"
)

func main() {
	authService := auth.NewService("devorbit-secret-key")

	http.HandleFunc("/api/v1/auth/pair", func(w http.ResponseWriter, r *http.Request) {
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

	log.Println("Control Plane listening on :8080...")
}
