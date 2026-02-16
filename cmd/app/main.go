package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"filetransfer/internal/api"
	"filetransfer/internal/config"
	"filetransfer/internal/discovery"
	"filetransfer/internal/storage"
	"filetransfer/internal/transfer"
	"filetransfer/pkg/utils"
)

//go:embed web/templates/* web/static/*
var content embed.FS

func main() {
	// Command-line flags
	webPort := flag.Int("web", 8080, "Web UI port")
	transferPort := flag.Int("transfer", 9000, "File transfer port")
	deviceName := flag.String("name", "", "Device name (defaults to hostname)")
	downloadDir := flag.String("downloads", "./downloads", "Download directory")
	flag.Parse()

	// Hostname default
	hostname, _ := os.Hostname()
	finalDeviceName := hostname
	if *deviceName != "" {
		finalDeviceName = *deviceName
	}

	// Configuration
	cfg := config.Config{
		ServerPort:    *webPort,
		TransferPort:  *transferPort,
		DiscoveryPort: 9001, // Fixed for all devices
		ChunkSize:     65536,
		DownloadDir:   *downloadDir,
		DeviceName:    finalDeviceName,
		BroadcastInt:  3 * time.Second,
	}

	// Setup directories
	os.MkdirAll(cfg.DownloadDir, 0755)

	// Utilities
	localIP := utils.GetLocalIP() // Or use GetOutboundIP() for better accuracy?
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	deviceID := fmt.Sprintf("%s-%d", localIP, time.Now().UnixNano())

	log.Printf("Starting FileTransfer on %s (%s)", localIP, cfg.DeviceName)

	// Services
	store := storage.NewStore(filepath.Join(".", fmt.Sprintf("data_%d", cfg.ServerPort))) // Separate data dir for instances if running multiple?
	// Note: previous implementation used "data" for all.
	// But if running multiple instances locally, they share "data/users.json". This is GOOD (shared users).
	// But "history"? Shared history might be confusing if they are "different devices".
	// But user requirement was "run simple test on one device".
	// If I use "data", they share everything.
	// Previously in `store.go`, I hardcoded `filepath.Join(dataDir, ...)` and passed `filepath.Join(".", "data")` in main.
	// For testing, I should probably separate them if I want them to act as distinct devices with distinct history?
	// But "Users" should be shared?
	// Let's stick to "data" folder for now, but maybe use "data_<PORT>" to avoid locking issues on JSON files if we run them concurrently?
	// `store.go` uses `ioutil.WriteFile` (atomic write usually). `users.json` read/write might race.
	// To be safe for the "Multiple Instances Test", I'll use separate data dirs based on port.
	// Or just "data" and hope for the best (usually fine for read-mostly users, write-heavy history might interleave).
	// I'll use unique data dir to prevent conflicts in this testing scenario.
	dataDir := fmt.Sprintf("data_%d", cfg.ServerPort)
	store = storage.NewStore(dataDir)

	// API Server (needs to be created first to get Broadcast ref, or circular dep?)
	// Server needs TransferService. TransferService needs Broadcast.
	// I'll create Server first, then TransferService, then set TransferService on Server.

	apiServer := api.NewServer(cfg, store, nil, localIP, content)

	// Discovery Service
	// Needs to get current username from Server logic
	discoveryService := discovery.NewService(cfg, localIP, deviceID, apiServer.GetUsername)

	// Transfer Service
	// Broadcasts via API Server's WebSocket
	transferService := transfer.NewService(cfg, deviceID, store, discoveryService, apiServer.Broadcast)

	// Wire up circular dependencies
	apiServer.SetTransferService(transferService)
	apiServer.SetDiscoveryService(discoveryService)

	// Start Services
	discoveryService.Start()
	transferService.Start()

	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════╗\n")
	fmt.Printf("║           FileTransfer - Ready!                  ║\n")
	fmt.Printf("╠══════════════════════════════════════════════════╣\n")
	fmt.Printf("║  Device: %-40s║\n", cfg.DeviceName)
	fmt.Printf("║  Local IP: %-38s║\n", localIP)
	fmt.Printf("║  Web UI: http://localhost:%-23d║\n", cfg.ServerPort)
	fmt.Printf("║  Transfer Port: %-33d║\n", cfg.TransferPort)
	fmt.Printf("║  Data Dir: %-38s║\n", dataDir)
	fmt.Printf("╚══════════════════════════════════════════════════╝\n")
	fmt.Printf("\n")

	log.Fatal(apiServer.Start())
}
