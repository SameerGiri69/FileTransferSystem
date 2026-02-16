package transfer

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

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
	mu        sync.RWMutex
}

func NewService(cfg config.Config, deviceID string, store *storage.Store, disc *discovery.Service, broadcast func(string, interface{})) *Service {
	return &Service{
		config:    cfg,
		deviceID:  deviceID,
		store:     store,
		discovery: disc,
		broadcast: broadcast,
		transfers: make(map[string]*models.Transfer),
	}
}

func (s *Service) Start() {
	go s.listenTCP()
}

func (s *Service) listenTCP() {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.config.TransferPort))
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept error:", err)
			continue
		}
		go s.handleIncomingTransfer(conn)
	}
}

func (s *Service) handleIncomingTransfer(conn net.Conn) {
	defer conn.Close()

	// 1. Read metadata
	var metadata struct {
		ID       string `json:"id"`
		FileName string `json:"fileName"`
		FileSize int64  `json:"fileSize"`
		PeerID   string `json:"peerId"` // Sender's ID
		PeerName string `json:"peerName"`
	}

	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&metadata); err != nil {
		return
	}

	// 2. Create Transfer object
	transfer := &models.Transfer{
		ID:        metadata.ID,
		FileName:  metadata.FileName,
		FileSize:  metadata.FileSize,
		Direction: "receive",
		PeerID:    metadata.PeerID,
		PeerName:  metadata.PeerName,
		Status:    "receiving",
		StartTime: time.Now(),
		Conn:      conn,
	}

	s.mu.Lock()
	s.transfers[transfer.ID] = transfer
	s.mu.Unlock()

	s.broadcast("transferUpdate", transfer)

	// 3. Receive file content
	savePath := filepath.Join(s.config.DownloadDir, metadata.FileName)
	file, err := os.Create(savePath)
	if err != nil {
		log.Println("Error creating file:", err)
		return
	}
	defer file.Close()

	// Buffer for reading chunks
	buf := make([]byte, s.config.ChunkSize)
	reader := conn // conn is an io.Reader

	lastUpdate := time.Now()

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			file.Write(buf[:n])
			transfer.Transferred += int64(n)
			transfer.Progress = float64(transfer.Transferred) / float64(transfer.FileSize) * 100

			// Update speed every second
			if time.Since(lastUpdate) > time.Second {
				elapsed := time.Since(transfer.StartTime).Seconds()
				if elapsed > 0 {
					transfer.Speed = float64(transfer.Transferred) / 1024 / 1024 / elapsed
				}
				s.broadcast("transferUpdate", transfer)
				lastUpdate = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Println("Transfer error:", err)
			transfer.Status = "failed"
			s.broadcast("transferUpdate", transfer)
			return
		}
	}

	// 4. Verify (Optional, skipping heavy checksum for now or implementing if needed)
	// Simplified for now: Send ACK?

	// 5. Complete
	transfer.Status = "completed"
	transfer.Progress = 100
	s.broadcast("transferUpdate", transfer)

	s.store.AddHistory(&models.TransferHistory{
		ID:        transfer.ID,
		FileName:  transfer.FileName,
		FileSize:  transfer.FileSize,
		Direction: "receive",
		PeerName:  transfer.PeerName,
		Timestamp: time.Now(),
		Status:    "completed",
	})

	log.Printf("File received: %s", metadata.FileName)
}

func (s *Service) SendFile(peerID, filePath string) error {
	peer, ok := s.discovery.GetDevice(peerID)
	if !ok {
		return fmt.Errorf("peer not found")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	stat, _ := file.Stat()
	fileName := filepath.Base(filePath)
	transferID := fmt.Sprintf("mk-%d", time.Now().UnixNano())

	// Connect to peer
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", peer.IP, peer.Port))
	if err != nil {
		return err
	}
	defer conn.Close() // In a real app, keep open until confirmed?

	// Create Transfer Object
	transfer := &models.Transfer{
		ID:        transferID,
		FileName:  fileName,
		FileSize:  stat.Size(),
		Direction: "send",
		PeerID:    peer.ID,
		PeerName:  peer.Name,
		Status:    "sending",
		StartTime: time.Now(),
		Conn:      conn,
	}

	s.mu.Lock()
	s.transfers[transferID] = transfer
	s.mu.Unlock()

	s.broadcast("transferUpdate", transfer)

	// Send Metadata
	metadata := map[string]interface{}{
		"id":       transferID,
		"fileName": fileName,
		"fileSize": stat.Size(),
		"peerId":   s.deviceID,
		// Wait, I need my Device Name/ID to send to peer.
		// I don't have access to my DeviceID here easily unless passed.
		// I'll use config.DeviceName and pass ID in Service struct or Config?
		// discovery service knows the ID.
		"peerName": s.config.DeviceName,
	}
	// Note: peerId in metadata is Sender's ID.
	// How do I get MY device ID?
	// I'll add DeviceID to Service struct (it wasn't in NewService args but I need it).
	// Let's assume I pass it in NewService or Config.

	// Actually I'll use a placeholder for now or fix NewService.
	// I will fix NewService to accept DeviceID.

	json.NewEncoder(conn).Encode(metadata)

	// Stream File
	buf := make([]byte, s.config.ChunkSize)
	lastUpdate := time.Now()

	for {
		n, err := file.Read(buf)
		if n > 0 {
			_, wErr := conn.Write(buf[:n])
			if wErr != nil {
				return wErr
			}
			transfer.Transferred += int64(n)
			transfer.Progress = float64(transfer.Transferred) / float64(transfer.FileSize) * 100

			if time.Since(lastUpdate) > time.Second {
				elapsed := time.Since(transfer.StartTime).Seconds()
				if elapsed > 0 {
					transfer.Speed = float64(transfer.Transferred) / 1024 / 1024 / elapsed
				}
				s.broadcast("transferUpdate", transfer)
				lastUpdate = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	transfer.Status = "completed"
	transfer.Progress = 100
	s.broadcast("transferUpdate", transfer)

	s.store.AddHistory(&models.TransferHistory{
		ID:        transfer.ID,
		FileName:  transfer.FileName,
		FileSize:  transfer.FileSize,
		Direction: "send",
		PeerName:  transfer.PeerName,
		Timestamp: time.Now(),
		Status:    "completed",
	})

	log.Printf("File sent: %s", fileName)
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

func calculateChecksum(path string) string {
	f, _ := os.Open(path)
	defer f.Close()
	h := md5.New()
	io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil))
}
