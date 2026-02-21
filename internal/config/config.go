package config

import "time"

type Config struct {
	ServerPort    int
	TransferPort  int
	DiscoveryPort int
	ChunkSize     int
	DownloadDir   string
	DeviceName    string
	BroadcastInt  time.Duration
	DBConnStr     string
	SMTPFrom      string
	SMTPPass      string
}
