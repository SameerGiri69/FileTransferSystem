package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"filetransfer/internal/api"
	"filetransfer/internal/config"
	"filetransfer/internal/discovery"
	"filetransfer/internal/storage"
	"filetransfer/internal/transfer"
	"filetransfer/pkg/utils"
	"filetransfer/web"
)

func main() {
	webPort := flag.Int("web", 8080, "Web UI port")
	transferPort := flag.Int("transfer", 9000, "File transfer TCP port")
	deviceName := flag.String("name", "", "Device name (defaults to hostname)")
	flag.Parse()

	// Device name
	hostname, _ := os.Hostname()
	finalName := hostname
	if *deviceName != "" {
		finalName = *deviceName
	}

	// Downloads dir → user's ~/Downloads
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	downloadDir := homeDir + "/Downloads"
	os.MkdirAll(downloadDir, 0755)

	// SMTP config — env overrides, fallback to defaults
	smtpFrom := getEnv("SMTP_FROM", "filetransfer@example.com")
	smtpPass := getEnv("SMTP_PASS", "dyhz zlfe ejma xnna") // Gmail App Password

	// PostgreSQL DSN — env override or default
	dbDSN := getEnv("DATABASE_URL",
		"host=127.0.0.1 port=5432 user=sameer password=Sameer@123 dbname=filetransfer sslmode=disable")

	cfg := config.Config{
		ServerPort:    *webPort,
		TransferPort:  *transferPort,
		DiscoveryPort: 9001,
		ChunkSize:     65536,
		DownloadDir:   downloadDir,
		DeviceName:    finalName,
		BroadcastInt:  3 * time.Second,
		DBConnStr:     dbDSN,
		SMTPFrom:      smtpFrom,
		SMTPPass:      smtpPass,
	}

	// Storage (Postgres)
	store, err := storage.NewStore(dbDSN)
	if err != nil {
		log.Fatalf("Cannot connect to database: %v\n  DSN: %s\n  Tip: set DATABASE_URL env var to override.", err, dbDSN)
	}
	log.Println("Connected to PostgreSQL database ✓")

	// Network
	localIP := utils.GetLocalIP()
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	deviceID := fmt.Sprintf("%s-%d", localIP, time.Now().UnixNano())

	// Wire up services
	// API server created first so we can pass GetUsername to discovery
	apiServer := api.NewServer(cfg, store, nil, nil, localIP, web.FS)

	discSvc := discovery.NewService(cfg, localIP, deviceID, apiServer.GetUsername)

	transferSvc := transfer.NewService(cfg, deviceID, store, discSvc, apiServer.Broadcast, apiServer.GetUsername)

	apiServer.SetDiscovery(discSvc)
	apiServer.SetTransfer(transferSvc)

	// Start background services
	discSvc.Start()
	transferSvc.Start()

	printBanner(cfg, localIP, downloadDir)

	log.Fatal(apiServer.Start())
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func printBanner(cfg config.Config, localIP, downloadDir string) {
	fmt.Printf("\n")
	fmt.Printf("╔══════════════════════════════════════════════════════╗\n")
	fmt.Printf("║              FileTransfer  — Ready!                  ║\n")
	fmt.Printf("╠══════════════════════════════════════════════════════╣\n")
	fmt.Printf("║  Device   : %-40s║\n", cfg.DeviceName)
	fmt.Printf("║  Local IP : %-40s║\n", localIP)
	fmt.Printf("║  Web UI   : http://localhost:%-25d║\n", cfg.ServerPort)
	fmt.Printf("║  Downloads: %-40s║\n", downloadDir)
	fmt.Printf("╚══════════════════════════════════════════════════════╝\n\n")
}
