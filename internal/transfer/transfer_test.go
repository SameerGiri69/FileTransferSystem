package transfer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"filetransfer/internal/config"
	"filetransfer/internal/models"
)

func TestReceiveFileBufferAndWhitespaceFix(t *testing.T) {
	// Setup temporary download directory
	tmpDir, err := os.MkdirTemp("", "transfer_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.Config{
		DownloadDir:  tmpDir,
		TransferPort: 0,
		ChunkSize:    1024,
	}

	s := NewService(cfg, "test-device", nil, nil, func(s string, i interface{}) {}, func() string { return "test@example.com" })

	fileName := "test.png"
	fileData := []byte("pagedata-simulating-image-bytes-which-should-not-be-lost")
	fileSize := int64(len(fileData))
	transferID := "test-id"

	meta := wireMetadata{
		ID:         transferID,
		FileName:   fileName,
		FileSize:   fileSize,
		SenderID:   "sender-id",
		SenderName: "sender-name",
	}

	// Create a buffer that contains JSON, a newline, and then file data
	// This simulates the issue where a newline added by json.Encoder.Encode
	// or another source gets prepended to the file data.
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(meta) // Adds a newline
	buf.Write(fileData)

	// Simulate a connection
	pr, pw := net.Pipe()
	defer pr.Close()

	go func() {
		pw.Write(buf.Bytes())
		pw.Close()
	}()

	// Decoding logic from handleIncoming
	reader := bufio.NewReader(pr)
	decoder := json.NewDecoder(reader)

	var decodedMeta wireMetadata
	if err := decoder.Decode(&decodedMeta); err != nil {
		t.Fatalf("Failed to decode metadata: %v", err)
	}

	// Combined reader from handleIncoming
	combinedReader := io.MultiReader(decoder.Buffered(), reader)

	// Call receiveFile - it should now handle the buffered data and skip the newline
	s.receiveFile(pr, combinedReader, decodedMeta)

	// Verify the file content
	savedPath := filepath.Join(tmpDir, fileName)
	savedData, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	if !bytes.Equal(savedData, fileData) {
		t.Errorf("Saved data mismatch.\nExpected: %q\nGot:      %q", string(fileData), string(savedData))
	}
}

func TestDeduplication(t *testing.T) {
	s := NewService(config.Config{}, "test-device", nil, nil, func(s string, i interface{}) {}, func() string { return "test@example.com" })

	transferID := "duplicate-id"
	pt := &models.PendingTransfer{ID: transferID}

	s.mu.Lock()
	s.pending[transferID] = pt
	s.mu.Unlock()

	// Simulate incoming connection with same ID
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()

	go func() {
		conn, _ := net.Dial("tcp", l.Addr().String())
		json.NewEncoder(conn).Encode(wireMetadata{ID: transferID})
		conn.Close()
	}()

	conn, _ := l.Accept()
	s.handleIncoming(conn) // This should return early due to deduplication

	s.mu.RLock()
	_, ok := s.pending[transferID]
	s.mu.RUnlock()

	if !ok {
		t.Error("Pending transfer was incorrectly removed or not found")
	}
}
