package storage

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"filetransfer/internal/models"
)

type Store struct {
	mu          sync.RWMutex
	dataDir     string
	usersPath   string
	historyPath string
	Users       map[string]*models.User            `json:"users"`
	History     map[string]*models.TransferHistory `json:"history"`
}

func NewStore(dataDir string) *Store {
	os.MkdirAll(dataDir, 0755)

	store := &Store{
		dataDir:     dataDir,
		usersPath:   filepath.Join(dataDir, "users.json"),
		historyPath: filepath.Join(dataDir, "history.json"),
		Users:       make(map[string]*models.User),
		History:     make(map[string]*models.TransferHistory),
	}

	store.load()
	return store
}

func (s *Store) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if data, err := ioutil.ReadFile(s.usersPath); err == nil {
		var users map[string]*models.User
		if err := json.Unmarshal(data, &users); err == nil {
			s.Users = users
		}
	}

	if data, err := ioutil.ReadFile(s.historyPath); err == nil {
		var history map[string]*models.TransferHistory
		if err := json.Unmarshal(data, &history); err == nil {
			s.History = history
		}
	}
}

func (s *Store) saveUsers() error {
	data, err := json.MarshalIndent(s.Users, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(s.usersPath, data, 0644)
}

func (s *Store) saveHistory() error {
	data, err := json.MarshalIndent(s.History, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(s.historyPath, data, 0644)
}

func (s *Store) RegisterUser(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.Users[username]; exists {
		return os.ErrExist
	}

	s.Users[username] = &models.User{
		Username:  username,
		Password:  password,
		CreatedAt: time.Now(),
	}

	return s.saveUsers()
}

func (s *Store) LoginUser(username, password string) (*models.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.Users[username]
	if !exists || user.Password != password {
		return nil, false
	}

	return user, true
}

func (s *Store) AddHistory(item *models.TransferHistory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.History[item.ID] = item
	return s.saveHistory()
}

func (s *Store) GetHistory() []*models.TransferHistory {
	s.mu.RLock()
	defer s.mu.RUnlock()

	history := make([]*models.TransferHistory, 0, len(s.History))
	for _, item := range s.History {
		history = append(history, item)
	}
	return history
}
