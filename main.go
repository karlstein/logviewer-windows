package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"logviewer/profile"
	"logviewer/sshclient"
)

//go:embed static
var staticFS embed.FS

// staticHandler is how static files are served. Default is embedded.
// The `go build -tags dev` variant overrides this to serve from disk
// for live-reload during development.
var staticHandler http.Handler

func init() {
	staticSub, _ := fs.Sub(staticFS, "static")
	staticHandler = http.FileServer(http.FS(staticSub))
}

// Hub manages SSE clients for log streaming.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

func newHub() *Hub {
	return &Hub{clients: make(map[chan string]struct{})}
}

func (h *Hub) subscribe() chan string {
	ch := make(chan string, 1000)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *Hub) broadcast(line string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- line:
		default:
			// Drop for slow clients
		}
	}
}

// statusHub manages status SSE clients.
type statusHub struct {
	mu      sync.RWMutex
	clients map[chan StatusUpdate]struct{}
}

type StatusUpdate struct {
	Running bool   `json:"running"`
	Message string `json:"message,omitempty"`
}

func newStatusHub() *statusHub {
	return &statusHub{clients: make(map[chan StatusUpdate]struct{})}
}

func (h *statusHub) subscribe() chan StatusUpdate {
	ch := make(chan StatusUpdate, 10)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *statusHub) unsubscribe(ch chan StatusUpdate) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *statusHub) broadcast(s StatusUpdate) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- s:
		default:
		}
	}
}

// Server holds application state.
type Server struct {
	profiles []profile.Profile

	client  *sshclient.Client
	running bool
	stop    context.CancelFunc
	runMu   sync.Mutex

	logHub    *Hub
	statusHub *statusHub
}

// runServer creates the HTTP server, starts it on a random localhost port,
// and returns the port number, the server, and the application state.
func runServer() (int, *http.Server, *Server) {
	profiles, err := profile.Load()
	if err != nil {
		log.Printf("Warning: could not load profiles: %v", err)
		profiles = []profile.Profile{}
	}

	srv := &Server{
		profiles:  profiles,
		logHub:    newHub(),
		statusHub: newStatusHub(),
	}

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/profiles", srv.handleProfiles)
	mux.HandleFunc("/api/profiles/", srv.handleProfileByID)
	mux.HandleFunc("/api/run", srv.handleRun)
	mux.HandleFunc("/api/stop", srv.handleStop)
	mux.HandleFunc("/api/stream", srv.handleStream)
	mux.HandleFunc("/api/status", srv.handleStatus)
		// Serve the SPA (from embed in production, or disk in dev mode)
		mux.Handle("/", staticHandler)


	// Listen on a random available port (127.0.0.1 only for security)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	httpServer := &http.Server{Handler: mux}

	// Start HTTP server in background
	go func() {
		log.Printf("Server listening on 127.0.0.1:%d", port)
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for the HTTP server to be ready
	waitForServer(port)
	return port, httpServer, srv
}

// cleanup shuts down the HTTP server and closes the SSH client.
func cleanup(httpServer *http.Server, srv *Server) {
	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
	if srv.client != nil {
		srv.client.Close()
	}
	log.Println("Shutdown complete")
}

// waitForServer polls the HTTP server until it accepts connections.
func waitForServer(port int) {
	time.Sleep(50 * time.Millisecond) // initial short wait
	for i := 0; i < 100; i++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	log.Println("Warning: server may not be ready yet")
}

// ------------------- Profile handlers -------------------

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listProfiles(w, r)
	case http.MethodPost:
		s.createProfile(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listProfiles(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.profiles)
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	var p profile.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := p.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profs, err := profile.Add(s.profiles, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.profiles = profs
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func (s *Server) handleProfileByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 || id >= len(s.profiles) {
		http.Error(w, "invalid profile ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		s.updateProfile(w, r, id)
	case http.MethodDelete:
		s.deleteProfile(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request, id int) {
	var p profile.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	profs, err := profile.Update(s.profiles, id, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	s.profiles = profs
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func (s *Server) deleteProfile(w http.ResponseWriter, _ *http.Request, id int) {
	profs, err := profile.Delete(s.profiles, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.profiles = profs
	w.WriteHeader(http.StatusNoContent)
}

// ------------------- Command handlers -------------------

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	if s.running {
		http.Error(w, "a command is already running", http.StatusConflict)
		return
	}

	var req struct {
		ProfileIndex int    `json:"profile_index"`
		Command      string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ProfileIndex < 0 || req.ProfileIndex >= len(s.profiles) {
		http.Error(w, "invalid profile index", http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	p := s.profiles[req.ProfileIndex]

	// Close old client
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}

	// Connect via SSH
	client, err := sshclient.Connect(p.SSHAddr(), p.User, p.Password, p.KeyPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("SSH connection failed: %v", err), http.StatusBadGateway)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.client = client
	s.stop = cancel
	s.running = true

	s.statusHub.broadcast(StatusUpdate{Running: true, Message: fmt.Sprintf("Running on %s...", p.Name)})

	if err := client.RunCommand(ctx, req.Command, func(line string) {
		s.logHub.broadcast(line)
	}); err != nil {
		s.running = false
		cancel()
		client.Close()
		s.statusHub.broadcast(StatusUpdate{Running: false, Message: "Command failed: " + err.Error()})
		http.Error(w, "command failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Monitor completion in background
	go func() {
		client.Wait() // blocks until command finishes naturally
		s.runMu.Lock()
		s.running = false
		s.runMu.Unlock()
		s.statusHub.broadcast(StatusUpdate{Running: false, Message: "Command completed"})
	}()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.runMu.Lock()
	defer s.runMu.Unlock()

	if !s.running || s.client == nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "not_running"})
		return
	}

	// Stop in background
	client := s.client
	cancel := s.stop
	s.running = false

	if cancel != nil {
		cancel()
	}
	go func() {
		client.Close()
	}()

	s.statusHub.broadcast(StatusUpdate{Running: false, Message: "Stopped"})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// ------------------- SSE handlers -------------------

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.logHub.subscribe()
	defer s.logHub.unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			// SSE format: "data: <line>\n\n"
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial state
	s.runMu.Lock()
	initial := StatusUpdate{Running: s.running}
	if s.running {
		initial.Message = "Running..."
	}
	s.runMu.Unlock()
	data, _ := json.Marshal(initial)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	ch := s.statusHub.subscribe()
	defer s.statusHub.unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case st, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(st)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
