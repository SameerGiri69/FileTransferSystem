package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"filetransfer/internal/config"
	"filetransfer/internal/discovery"
	"filetransfer/internal/models"
	"filetransfer/internal/storage"
	"filetransfer/internal/transfer"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	config     config.Config
	store      *storage.Store
	disc       *discovery.Service
	transfer   *transfer.Service
	webContent embed.FS
	localIP    string

	wsClients map[*websocket.Conn]bool
	wsMu      sync.Mutex

	mu          sync.RWMutex
	currentUser *models.User // logged-in user for this instance
}

func NewServer(
	cfg config.Config,
	store *storage.Store,
	disc *discovery.Service,
	ts *transfer.Service,
	localIP string,
	content embed.FS,
) *Server {
	return &Server{
		config:     cfg,
		store:      store,
		disc:       disc,
		transfer:   ts,
		localIP:    localIP,
		webContent: content,
		wsClients:  make(map[*websocket.Conn]bool),
	}
}

// SetDiscovery wires the discovery service (called after NewServer to resolve circular dep).
func (s *Server) SetDiscovery(d *discovery.Service) { s.disc = d }

// SetTransfer wires the transfer service.
func (s *Server) SetTransfer(t *transfer.Service) { s.transfer = t }

// GetUsername returns the email of the currently logged-in user (used by discovery).
func (s *Server) GetUsername() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.currentUser != nil {
		return s.currentUser.Email
	}
	return ""
}

// Broadcast sends a JSON message to all connected WebSocket clients.
func (s *Server) Broadcast(msgType string, payload interface{}) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	msg := map[string]interface{}{"type": msgType, "payload": payload}
	for conn := range s.wsClients {
		if err := conn.WriteJSON(msg); err != nil {
			conn.Close()
			delete(s.wsClients, conn)
		}
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Auth (no middleware)
	mux.HandleFunc("/api/auth/register", s.handleRegister)
	mux.HandleFunc("/api/auth/login", s.handleLogin)
	mux.HandleFunc("/api/auth/logout", s.requireAuth(s.handleLogout))

	// App (auth required)
	mux.HandleFunc("/api/devices", s.requireAuth(s.handleDevices))
	mux.HandleFunc("/api/transfer/send", s.requireAuth(s.handleSend))
	mux.HandleFunc("/api/transfer/accept", s.requireAuth(s.handleAccept))
	mux.HandleFunc("/api/transfer/reject", s.requireAuth(s.handleReject))
	mux.HandleFunc("/api/transfers/active", s.requireAuth(s.handleActiveTransfers))
	mux.HandleFunc("/api/history", s.requireAuth(s.handleHistory))
	mux.HandleFunc("/api/files", s.requireAuth(s.handleFiles))
	mux.HandleFunc("/api/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("/ws", s.handleWS)

	// Static
	staticFS, _ := fs.Sub(s.webContent, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Downloads (auth required)
	mux.HandleFunc("/dl/", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/dl/", http.FileServer(http.Dir(s.config.DownloadDir))).ServeHTTP(w, r)
	}))

	// Catch-all: serve SPA or redirect to auth
	mux.HandleFunc("/", s.handleIndex)

	addr := fmt.Sprintf(":%d", s.config.ServerPort)
	log.Printf("Web UI listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

// ---- Middleware ----

func (s *Server) sessionUser(r *http.Request) *models.User {
	cookie, err := r.Cookie(s.cookieName())
	if err != nil {
		return nil
	}
	email, ok := s.store.GetSession(cookie.Value)
	if !ok {
		log.Printf("[AUTH] Session not found for token: %s (maybe server restarted?)", cookie.Value)
		return nil
	}
	u, err := s.store.GetUserByEmail(email)
	if err != nil {
		log.Printf("[AUTH] User %s not found in DB", email)
		return nil
	}
	s.mu.Lock()
	s.currentUser = u
	s.mu.Unlock()
	return u
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.sessionUser(r)
		if u == nil {
			log.Printf("[AUTH] Unauthorized request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ---- Page Handler ----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	user := s.sessionUser(r)
	if user == nil {
		// Serve auth page
		data, err := s.webContent.ReadFile("templates/auth.html")
		if err != nil {
			http.Error(w, "Template not found", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
		return
	}
	data, err := s.webContent.ReadFile("templates/index.html")
	if err != nil {
		http.Error(w, "Template not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

// ---- Auth Handlers ----

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request", 400)
		return
	}
	if body.Email == "" || body.Password == "" {
		jsonError(w, "Email and password required", 400)
		return
	}
	if err := s.store.RegisterUser(body.Email, body.Password); err != nil {
		jsonError(w, "Email already registered", 400)
		return
	}

	token := s.store.CreateSession(body.Email)
	http.SetCookie(w, s.sessionCookie(token))

	u, _ := s.store.GetUserByEmail(body.Email)
	s.mu.Lock()
	s.currentUser = u
	s.mu.Unlock()

	log.Printf("[AUTH] New registration & login: %s", body.Email)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "email": body.Email})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request", 400)
		return
	}
	user, err := s.store.AuthenticateUser(body.Email, body.Password)
	if err != nil {
		jsonError(w, err.Error(), 401)
		return
	}
	token := s.store.CreateSession(user.Email)
	http.SetCookie(w, s.sessionCookie(token))

	s.mu.Lock()
	s.currentUser = user
	s.mu.Unlock()

	log.Printf("[AUTH] Logged in: %s", user.Email)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "email": user.Email})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(s.cookieName())
	if err == nil {
		s.store.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    s.cookieName(),
		Value:   "",
		Expires: time.Unix(0, 0),
		Path:    "/",
	})
	jsonOK(w, "logged out")
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := s.sessionUser(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"email":      user.Email,
		"deviceName": s.config.DeviceName,
		"localIP":    s.localIP,
	})
}

// ---- App Handlers ----

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.disc.GetDevices()
	w.Header().Set("Content-Type", "application/json")
	if devices == nil {
		devices = []*models.Device{}
	}
	json.NewEncoder(w).Encode(devices)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		log.Println("Form error:", err)
		jsonError(w, "File upload error", 400)
		return
	}

	deviceID := r.FormValue("deviceId")
	if deviceID == "" {
		jsonError(w, "deviceId required", 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(w, "file required", 400)
		return
	}
	defer file.Close()

	// Use a safer temp filename
	safeName := filepath.Base(header.Filename)
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("upload_%d_%s", time.Now().UnixNano(), safeName))

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		log.Println("Temp file create error:", err)
		jsonError(w, "could not create temp file", 500)
		return
	}

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		jsonError(w, "could not write temp file", 500)
		return
	}
	tmpFile.Close()

	log.Printf("Initiating transfer to %s: %s (%d bytes)", deviceID, safeName, header.Size)

	go func() {
		defer os.Remove(tmpPath)
		if err := s.transfer.SendFile(deviceID, tmpPath, safeName); err != nil {
			log.Println("Send error:", err)
		}
	}()

	jsonOK(w, "transfer initiated")
}

func (s *Server) handleAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var body struct {
		TransferID string `json:"transferId"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.transfer.AcceptTransfer(body.TransferID); err != nil {
		jsonError(w, err.Error(), 404)
		return
	}
	jsonOK(w, "accepted")
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var body struct {
		TransferID string `json:"transferId"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.transfer.RejectTransfer(body.TransferID); err != nil {
		jsonError(w, err.Error(), 404)
		return
	}
	jsonOK(w, "rejected")
}

func (s *Server) handleActiveTransfers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	transfers := s.transfer.GetTransfers()
	if transfers == nil {
		transfers = []*models.Transfer{}
	}
	json.NewEncoder(w).Encode(transfers)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	u := s.sessionUser(r)
	history, err := s.store.GetHistory(u.Email)
	if err != nil {
		jsonError(w, "DB error", 500)
		return
	}
	if history == nil {
		history = []*models.TransferHistory{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.config.DownloadDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	var files []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		files = append(files, map[string]interface{}{
			"name":      e.Name(),
			"size":      info.Size(),
			"timestamp": info.ModTime(),
		})
	}
	if files == nil {
		files = []map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.wsMu.Lock()
	s.wsClients[conn] = true
	s.wsMu.Unlock()

	// Keep alive — read pump to detect disconnects
	go func() {
		defer func() {
			s.wsMu.Lock()
			delete(s.wsClients, conn)
			s.wsMu.Unlock()
			conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

// ---- Helpers ----

func (s *Server) cookieName() string {
	return fmt.Sprintf("ft_session_%d", s.config.ServerPort)
}

func (s *Server) sessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Expires:  time.Now().Add(24 * time.Hour),
	}
}

func jsonOK(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": msg})
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
