package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	joinquant "github.com/sooboy/joinquant-api"
)

type fileSessionStore struct {
	mu   sync.Mutex
	path string
}

func (s *fileSessionStore) Load(_ context.Context, key string) (*joinquant.SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	states, err := s.read()
	if err != nil {
		return nil, err
	}
	state, ok := states[key]
	if !ok {
		return nil, joinquant.ErrSessionNotFound
	}
	return &state, nil
}

func (s *fileSessionStore) Save(_ context.Context, key string, state joinquant.SessionState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	states, err := s.read()
	if err != nil && !errors.Is(err, joinquant.ErrSessionNotFound) {
		return err
	}
	if states == nil {
		states = make(map[string]joinquant.SessionState)
	}
	states[key] = state
	return s.write(states)
}

func (s *fileSessionStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	states, err := s.read()
	if err != nil {
		return err
	}
	delete(states, key)
	return s.write(states)
}

func (s *fileSessionStore) read() (map[string]joinquant.SessionState, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, joinquant.ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	var states map[string]joinquant.SessionState
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, err
	}
	return states, nil
}

func (s *fileSessionStore) write(states map[string]joinquant.SessionState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".session-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, s.path)
}

func defaultSessionPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".joinquant-api-session.json"
	}
	return filepath.Join(directory, "joinquant-api", "sessions.json")
}
