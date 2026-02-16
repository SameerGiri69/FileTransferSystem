package discovery

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"filetransfer/internal/config"
	"filetransfer/internal/models"
)

type Service struct {
	config      config.Config
	localIP     string
	deviceID    string
	devices     map[string]*models.Device
	mu          sync.RWMutex
	getUsername func() string
}

func NewService(cfg config.Config, localIP, deviceID string, getUserName func() string) *Service {
	return &Service{
		config:      cfg,
		localIP:     localIP,
		deviceID:    deviceID,
		devices:     make(map[string]*models.Device),
		getUsername: getUserName,
	}
}

func (s *Service) Start() {
	go s.broadcastPresence()
	go s.listenDiscovery()
}

func (s *Service) broadcastPresence() {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("239.0.0.1:%d", s.config.DiscoveryPort))
	if err != nil {
		log.Fatal(err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Println("Broadcast error:", err)
		return
	}
	defer conn.Close()

	for {
		name := s.config.DeviceName
		username := s.getUsername()

		msg := map[string]interface{}{
			"id":       s.deviceID,
			"name":     name,
			"username": username,
			"ip":       s.localIP,
			"port":     s.config.TransferPort,
		}
		data, _ := json.Marshal(msg)
		if _, err := conn.Write(data); err != nil {
			log.Printf("Broadcast write error: %v", err)
		}
		time.Sleep(s.config.BroadcastInt)
	}
}

const maxDatagramSize = 8192

func (s *Service) listenDiscovery() {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("239.0.0.1:%d", s.config.DiscoveryPort))
	if err != nil {
		log.Fatal(err)
	}

	conn, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		log.Printf("Discovery listen error: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadBuffer(maxDatagramSize)

	buf := make([]byte, maxDatagramSize)
	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Read error: %v", err)
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		id, ok := msg["id"].(string)
		if !ok || id == s.deviceID {
			continue
		}

		// Update or add device
		s.mu.Lock()
		userName, _ := msg["username"].(string)

		s.devices[id] = &models.Device{
			ID:       id,
			Name:     msg["name"].(string),
			UserName: userName,
			IP:       srcAddr.IP.String(),
			Port:     int(msg["port"].(float64)),
			LastSeen: time.Now(),
		}
		s.mu.Unlock()
	}
}

func (s *Service) GetDevices() []*models.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()

	devices := make([]*models.Device, 0, len(s.devices))
	for _, dev := range s.devices {
		if time.Since(dev.LastSeen) < 10*time.Second {
			devices = append(devices, dev)
		}
	}
	return devices
}

func (s *Service) GetDevice(id string) (*models.Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dev, ok := s.devices[id]
	return dev, ok
}
