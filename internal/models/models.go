package models

import (
	"time"

	"github.com/gorilla/websocket"
)

type User struct {
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"createdAt"`
}

type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	IP       string    `json:"ip"`
	Port     int       `json:"port"`
	UserName string    `json:"username"`
	LastSeen time.Time `json:"lastSeen"`
}

type Transfer struct {
	ID          string      `json:"id"`
	FileName    string      `json:"fileName"`
	FileSize    int64       `json:"fileSize"`
	Transferred int64       `json:"transferred"`
	Progress    float64     `json:"progress"`
	Speed       float64     `json:"speed"`
	Status      string      `json:"status"`    // pending, in-progress, completed, failed
	Direction   string      `json:"direction"` // send, receive
	PeerID      string      `json:"peerId"`
	PeerName    string      `json:"peerName"`
	Conn        interface{} `json:"-"` // net.Conn, but avoid importing net in models if possible, or interface{}
	StartTime   time.Time   `json:"startTime"`
}

type TransferHistory struct {
	ID        string    `json:"id"`
	FileName  string    `json:"fileName"`
	FileSize  int64     `json:"fileSize"`
	Direction string    `json:"direction"`
	PeerName  string    `json:"peerName"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"`
}

type WSClient struct {
	Conn *websocket.Conn
}
