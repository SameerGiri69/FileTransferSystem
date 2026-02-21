package transfer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"filetransfer/internal/config"
	"filetransfer/internal/discovery"
	"filetransfer/internal/models"
	"filetransfer/internal/storage"
)

type Service struct {
	config    config.Config
	deviceID  string
	store     *storage.Store
	discovery *discovery.Service
	broadcast func(string, interface{})

	transfers map[string]*models.Transfer
	pending   map[string]*models.PendingTransfer
	mu        sync.RWMutex

	getUsername func() string
}

func NewService(
	cfg config.Config,
	deviceID string,
	store *storage.Store,
	disc *discovery.Service,
	broadcast func(string, interface{}),
	getUsername func() string,
) *Service {
	return &Service{
		config:      cfg,
		deviceID:    deviceID,
		store:       store,
		discovery:   disc,
		broadcast:   broadcast,
		transfers:   make(map[string]*models.Transfer),
		pending:     make(map[string]*models.PendingTransfer),
		getUsername: getUsername,
	}
}

func (s *Service) Start() {
	go s.listenTCP()
}

// ----- TCP Listener (Receiver Side) -----

func (s *Service) listenTCP() {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.config.TransferPort))
	if err != nil {
		log.Fatal("Transfer listen:", err)
	}
	defer ln.Close()
	log.Printf("Transfer listener on :%d", s.config.TransferPort)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept error:", err)
			continue
		}
		go s.handleIncoming(conn)
	}
}

type wireMetadata struct {
	ID         string `json:"id"`
	FileName   string `json:"fileName"`
	FileSize   int64  `json:"fileSize"`
	SenderID   string `json:"senderId"`
	SenderName string `json:"senderName"`
}

type wireResponse struct {
	Accept bool `json:"accept"`
}

func (s *Service) handleIncoming(conn net.Conn) {
	defer func() {
		// conn closed after accept/reject decision was acted on
	}()

	reader := bufio.NewReader(conn)
	var meta wireMetadata
	if err := json.NewDecoder(reader).Decode(&meta); err != nil {
		conn.Close()
		return
	}

	// Store pending transfer (conn stays open so we can write ACK later)
	pt := &models.PendingTransfer{
		ID:         meta.ID,
		FileName:   meta.FileName,
		FileSize:   meta.FileSize,
		SenderID:   meta.SenderID,
		SenderName: meta.SenderName,
		Response:   make(chan bool, 1),
	}

	s.mu.Lock()
	s.pending[meta.ID] = pt
	s.mu.Unlock()

	// Notify UI of incoming request
	s.broadcast("incoming_request", pt)

	// Wait for UI decision (timeout 2 minutes)
	var accepted bool
	select {
	case accepted = <-pt.Response:
	case <-time.After(2 * time.Minute):
		accepted = false
	}

	// Send response back to sender
	resp := wireResponse{Accept: accepted}
	json.NewEncoder(conn).Encode(resp)

	s.mu.Lock()
	delete(s.pending, meta.ID)
	s.mu.Unlock()

	if !accepted {
		conn.Close()
		s.broadcast("transfer_rejected", map[string]string{"id": meta.ID, "fileName": meta.FileName})
		return
	}

	// Accept → receive file
	s.receiveFile(conn, meta)
}

func (s *Service) receiveFile(conn net.Conn, meta wireMetadata) {
	defer conn.Close()

	savePath := filepath.Join(s.config.DownloadDir, meta.FileName)
	// Avoid overwriting: append a counter if file exists
	if _, err := os.Stat(savePath); err == nil {
		ext := filepath.Ext(meta.FileName)
		base := meta.FileName[:len(meta.FileName)-len(ext)]
		savePath = filepath.Join(s.config.DownloadDir, fmt.Sprintf("%s_%d%s", base, time.Now().UnixMilli(), ext))
	}

	file, err := os.Create(savePath)
	if err != nil {
		log.Println("Create file error:", err)
		return
	}
	defer file.Close()

	t := &models.Transfer{
		ID:        meta.ID,
		FileName:  meta.FileName,
		FileSize:  meta.FileSize,
		Direction: "receive",
		PeerID:    meta.SenderID,
		PeerName:  meta.SenderName,
		Status:    "receiving",
		StartTime: time.Now(),
	}
	s.mu.Lock()
	s.transfers[t.ID] = t
	s.mu.Unlock()
	s.broadcast("transfer_update", t)

	buf := make([]byte, s.config.ChunkSize)
	lastUpdate := time.Now()

	for {
		n, err := conn.Read(buf)
		if n > 0 {
			file.Write(buf[:n])
			t.Transferred += int64(n)
			if t.FileSize > 0 {
				t.Progress = float64(t.Transferred) / float64(t.FileSize) * 100
			}
			if time.Since(lastUpdate) > time.Second {
				elapsed := time.Since(t.StartTime).Seconds()
				if elapsed > 0 {
					t.Speed = float64(t.Transferred) / 1024 / 1024 / elapsed
				}
				s.broadcast("transfer_update", t)
				lastUpdate = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Println("Receive error:", err)
			t.Status = "failed"
			s.broadcast("transfer_update", t)
			userEmail := s.getUsername()
			s.store.AddHistory(userEmail, &models.TransferHistory{
				ID:        t.ID,
				FileName:  t.FileName,
				FileSize:  t.FileSize,
				Direction: "receive",
				PeerName:  t.PeerName,
				Status:    "failed",
				Timestamp: time.Now(),
			})
			return
		}
	}

	t.Status = "completed"
	t.Progress = 100
	s.broadcast("transfer_update", t)

	userEmail := s.getUsername()
	s.store.AddHistory(userEmail, &models.TransferHistory{
		ID:        t.ID,
		FileName:  t.FileName,
		FileSize:  t.FileSize,
		Direction: "receive",
		PeerName:  t.PeerName,
		Status:    "completed",
		Timestamp: time.Now(),
	})

	log.Printf("Received file: %s from %s → %s", meta.FileName, meta.SenderName, savePath)
}

// ----- Sender Side -----

func (s *Service) SendFile(peerID, filePath, displayName string) error {
	peer, ok := s.discovery.GetDevice(peerID)
	if !ok {
		return fmt.Errorf("peer not found: %s", peerID)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, _ := file.Stat()
	fileName := displayName
	if fileName == "" {
		fileName = filepath.Base(filePath)
	}
	transferID := uuid.New().String()
	senderName := s.getUsername()

	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", peer.IP, peer.Port))
	if err != nil {
		return fmt.Errorf("dial peer: %w", err)
	}
	defer conn.Close()

	// Send metadata
	meta := wireMetadata{
		ID:         transferID,
		FileName:   fileName,
		FileSize:   stat.Size(),
		SenderID:   s.deviceID,
		SenderName: senderName,
	}
	if err := json.NewEncoder(conn).Encode(meta); err != nil {
		return fmt.Errorf("send metadata: %w", err)
	}

	t := &models.Transfer{
		ID:        transferID,
		FileName:  fileName,
		FileSize:  stat.Size(),
		Direction: "send",
		PeerID:    peer.ID,
		PeerName:  peer.Username,
		Status:    "waiting_acceptance",
		StartTime: time.Now(),
	}
	s.mu.Lock()
	s.transfers[transferID] = t
	s.mu.Unlock()
	s.broadcast("transfer_update", t)

	// Wait for receiver's accept/reject response
	conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
	var resp wireResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Status = "failed"
		s.broadcast("transfer_update", t)
		return fmt.Errorf("reading response: %w", err)
	}
	conn.SetReadDeadline(time.Time{}) // clear deadline

	if !resp.Accept {
		t.Status = "rejected"
		s.broadcast("transfer_update", t)
		userEmail := s.getUsername()
		s.store.AddHistory(userEmail, &models.TransferHistory{
			ID:        t.ID,
			FileName:  t.FileName,
			FileSize:  t.FileSize,
			Direction: "send",
			PeerName:  t.PeerName,
			Status:    "rejected",
			Timestamp: time.Now(),
		})
		return fmt.Errorf("receiver rejected the transfer")
	}

	// Accepted → stream the file
	t.Status = "sending"
	s.broadcast("transfer_update", t)

	buf := make([]byte, s.config.ChunkSize)
	lastUpdate := time.Now()

	for {
		n, err := file.Read(buf)
		if n > 0 {
			if _, wErr := conn.Write(buf[:n]); wErr != nil {
				t.Status = "failed"
				s.broadcast("transfer_update", t)
				return wErr
			}
			t.Transferred += int64(n)
			if t.FileSize > 0 {
				t.Progress = float64(t.Transferred) / float64(t.FileSize) * 100
			}
			if time.Since(lastUpdate) > time.Second {
				elapsed := time.Since(t.StartTime).Seconds()
				if elapsed > 0 {
					t.Speed = float64(t.Transferred) / 1024 / 1024 / elapsed
				}
				s.broadcast("transfer_update", t)
				lastUpdate = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Status = "failed"
			s.broadcast("transfer_update", t)
			return err
		}
	}

	t.Status = "completed"
	t.Progress = 100
	s.broadcast("transfer_update", t)

	userEmail := s.getUsername()
	s.store.AddHistory(userEmail, &models.TransferHistory{
		ID:        t.ID,
		FileName:  t.FileName,
		FileSize:  t.FileSize,
		Direction: "send",
		PeerName:  t.PeerName,
		Status:    "completed",
		Timestamp: time.Now(),
	})

	log.Printf("Sent file %s to %s", fileName, peer.Username)
	return nil
}

// AcceptTransfer signals the pending goroutine to accept and stream.
func (s *Service) AcceptTransfer(id string) error {
	s.mu.RLock()
	pt, ok := s.pending[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no pending transfer: %s", id)
	}
	pt.Response <- true
	return nil
}

// RejectTransfer signals the pending goroutine to reject.
func (s *Service) RejectTransfer(id string) error {
	s.mu.RLock()
	pt, ok := s.pending[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no pending transfer: %s", id)
	}
	pt.Response <- false
	return nil
}

func (s *Service) GetTransfers() []*models.Transfer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*models.Transfer, 0, len(s.transfers))
	for _, t := range s.transfers {
		list = append(list, t)
	}
	return list
}

func (s *Service) GetPending() []*models.PendingTransfer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*models.PendingTransfer, 0, len(s.pending))
	for _, p := range s.pending {
		list = append(list, p)
	}
	return list
}
