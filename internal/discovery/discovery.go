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

const (
	multicastAddr   = "239.0.0.1"
	maxDatagramSize = 8192
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
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", multicastAddr, s.config.DiscoveryPort))
	if err != nil {
		log.Fatal("resolve broadcast addr:", err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		log.Println("Broadcast dial error:", err)
		return
	}
	defer conn.Close()

	for {
		username := s.getUsername()
		// Only broadcast when logged in
		if username != "" {
			msg := map[string]interface{}{
				"id":       s.deviceID,
				"name":     s.config.DeviceName,
				"username": username,
				"ip":       s.localIP,
				"port":     s.config.TransferPort,
			}
			data, _ := json.Marshal(msg)
			if _, err := conn.Write(data); err != nil {
				log.Println("Broadcast write error:", err)
			}
		}
		time.Sleep(s.config.BroadcastInt)
	}
}

func (s *Service) listenDiscovery() {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", multicastAddr, s.config.DiscoveryPort))
	if err != nil {
		log.Fatal("resolve discovery addr:", err)
	}

	conn, err := net.ListenMulticastUDP("udp", nil, addr)
	if err != nil {
		log.Println("Discovery listen error:", err)
		return
	}
	defer conn.Close()
	conn.SetReadBuffer(maxDatagramSize)

	buf := make([]byte, maxDatagramSize)
	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Println("Discovery read error:", err)
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		id, _ := msg["id"].(string)
		if id == "" {
			continue
		}
		if id == s.deviceID {
			continue
		}

		username, _ := msg["username"].(string)
		name, _ := msg["name"].(string)
		log.Printf("[DISCOVERY] Found peer: %s (%s) from %s", username, name, srcAddr.String())
		portFloat, _ := msg["port"].(float64)

		s.mu.Lock()
		s.devices[id] = &models.Device{
			ID:       id,
			Name:     name,
			Username: username,
			IP:       srcAddr.IP.String(),
			Port:     int(portFloat),
			LastSeen: time.Now(),
		}
		s.mu.Unlock()
	}
}

// GetDevices returns devices seen in the last 10 seconds.
func (s *Service) GetDevices() []*models.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var devices []*models.Device
	for _, d := range s.devices {
		if time.Since(d.LastSeen) < 10*time.Second {
			devices = append(devices, d)
		}
	}
	return devices
}

func (s *Service) GetDevice(id string) (*models.Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[id]
	return d, ok
}
