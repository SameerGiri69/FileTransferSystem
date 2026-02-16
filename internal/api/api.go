package api

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gorilla/websocket"

	"filetransfer/internal/config"
	"filetransfer/internal/discovery"
	"filetransfer/internal/models"
	"filetransfer/internal/storage"
	"filetransfer/internal/transfer"
)

type Server struct {
	config      config.Config
	store       *storage.Store
	discovery   *discovery.Service
	transfer    *transfer.Service
	wsClients   map[*websocket.Conn]bool
	wsMu        sync.Mutex
	mu          sync.RWMutex
	currentUser *models.User
	localIP     string
	webContent  fs.FS
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func NewServer(cfg config.Config, store *storage.Store, disc *discovery.Service, ip string, webFS fs.FS) *Server {
	return &Server{
		config:     cfg,
		store:      store,
		discovery:  disc,
		localIP:    ip,
		webContent: webFS,
		wsClients:  make(map[*websocket.Conn]bool),
	}
}

func (s *Server) SetTransferService(ts *transfer.Service) {
	s.transfer = ts
}

func (s *Server) SetDiscoveryService(ds *discovery.Service) {
	s.discovery = ds
}

func (s *Server) GetCurrentUser() *models.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentUser
}

func (s *Server) GetUsername() string {
	user := s.GetCurrentUser()
	if user != nil {
		return user.Username
	}
	return ""
}

func (s *Server) Broadcast(msgType string, payload interface{}) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	msg := map[string]interface{}{
		"type":    msgType,
		"payload": payload,
	}

	for client := range s.wsClients {
		if err := client.WriteJSON(msg); err != nil {
			client.Close()
			delete(s.wsClients, client)
		}
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/transfers", s.handleTransfers)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/register", s.handleRegister)
	mux.HandleFunc("/api/logout", s.handleLogout)

	// Static Files
	staticFS, _ := fs.Sub(s.webContent, "web/static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Downloads
	mux.Handle("/dl/", http.StripPrefix("/dl/", http.FileServer(http.Dir(s.config.DownloadDir))))

	return http.ListenAndServe(fmt.Sprintf(":%d", s.config.ServerPort), mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	user := s.currentUser
	s.mu.RUnlock()

	tmplName := "web/templates/index.html"
	if user == nil {
		tmplName = "web/templates/login.html"
	}

	tmpl, err := template.ParseFS(s.webContent, tmplName)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	data := map[string]interface{}{
		"DeviceName": s.config.DeviceName,
		"LocalIP":    s.localIP,
		"ServerPort": s.config.ServerPort,
	}
	if user != nil {
		data["UserName"] = user.Username
	}
	tmpl.Execute(w, data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}

	user, success := s.store.LoginUser(creds.Username, creds.Password)
	if !success {
		http.Error(w, "Invalid credentials", 401)
		return
	}

	s.mu.Lock()
	s.currentUser = user
	s.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}

	if err := s.store.RegisterUser(creds.Username, creds.Password); err != nil {
		http.Error(w, "User already exists or error", 400)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.currentUser = nil
	s.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.discovery.GetDevices()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}

func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	transfers := s.transfer.GetTransfers()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transfers)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	history := s.store.GetHistory()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	user := s.currentUser
	s.mu.RUnlock()

	info := map[string]interface{}{
		"deviceName": s.config.DeviceName,
		"localIP":    s.localIP,
	}
	if user != nil {
		info["username"] = user.Username
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	deviceID := r.FormValue("deviceId")
	if deviceID == "" {
		http.Error(w, "Device ID required", 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File required", 400)
		return
	}
	defer file.Close()

	// Save temporarily
	tempPath := filepath.Join(os.TempDir(), header.Filename)
	tempFile, err := os.Create(tempPath)
	if err != nil {
		http.Error(w, "Failed to create temp file", 500)
		return
	}

	io.Copy(tempFile, file)
	tempFile.Close()

	// Send file in background
	go func() {
		defer os.Remove(tempPath)
		if err := s.transfer.SendFile(deviceID, tempPath); err != nil {
			fmt.Printf("Send file error: %v\n", err)
		}
	}()

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.wsMu.Lock()
	s.wsClients[conn] = true
	s.wsMu.Unlock()

	// Send initial data if needed, or wait for broadcast
}
