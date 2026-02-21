package models

import "time"

type User struct {
	ID           int       `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	Port     int       `json:"port"`
	Username string    `json:"username"`
	LastSeen time.Time `json:"lastSeen"`
}

// PendingTransfer holds an incoming transfer request awaiting user accept/reject
type PendingTransfer struct {
	ID         string `json:"id"`
	FileName   string `json:"fileName"`
	FileSize   int64  `json:"fileSize"`
	SenderID   string `json:"senderId"`
	SenderName string `json:"senderName"`
	// Channel to signal accept (true) or reject (false) back to the TCP goroutine
	Response chan bool `json:"-"`
}

type Transfer struct {
	ID          string    `json:"id"`
	FileName    string    `json:"fileName"`
	FileSize    int64     `json:"fileSize"`
	Transferred int64     `json:"transferred"`
	Progress    float64   `json:"progress"`
	Speed       float64   `json:"speed"` // MB/s
	Status      string    `json:"status"`
	Direction   string    `json:"direction"` // "send" | "receive"
	PeerID      string    `json:"peerId"`
	PeerName    string    `json:"peerName"`
	StartTime   time.Time `json:"startTime"`
}

type TransferHistory struct {
	ID        string    `json:"id"`
	UserEmail string    `json:"-"`
	FileName  string    `json:"fileName"`
	FileSize  int64     `json:"fileSize"`
	Direction string    `json:"direction"`
	PeerName  string    `json:"peerName"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"`
}

type ReceivedFile struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Sender    string    `json:"sender"`
	Timestamp time.Time `json:"timestamp"`
	Path      string    `json:"path"`
}
