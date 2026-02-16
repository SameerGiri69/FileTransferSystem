package config

import "time"

type Config struct {
	ServerPort    int
	TransferPort  int
	DiscoveryPort int
	ChunkSize     int64
	DownloadDir   string
	DeviceName    string
	BroadcastInt  time.Duration
}
