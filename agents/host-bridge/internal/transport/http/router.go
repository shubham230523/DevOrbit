package http

import (
	"net/http"
	"strings"
)

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/health", s.HandleHealth)
	mux.HandleFunc("/api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/workspaces/discover" {
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.HandleDiscoverWorkspaces(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleListWorkspaces(w, r)
	})

	mux.HandleFunc("/api/v1/files/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleListDirectory(w, r)
	})

	mux.HandleFunc("/api/v1/files/read", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleReadFile(w, r)
	})

	mux.HandleFunc("/api/v1/files/write", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleWriteFile(w, r)
	})

	mux.HandleFunc("/api/v1/git/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleGitStatus(w, r)
	})

	mux.HandleFunc("/api/v1/git/diff", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleGitDiff(w, r)
	})

	mux.HandleFunc("/api/v1/processes/execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleExecuteProcess(w, r)
	})

	mux.HandleFunc("/api/v1/processes/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/processes/") {
			s.HandleProcessById(w, r)
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/v1/devices/adb", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.HandleListADBDevices(w, r)
	})
}
