package telegrambot

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Store struct {
	Path string
}

func (s Store) Load() (State, error) {
	state := State{Chats: map[string]ChatState{}}
	if s.Path == "" {
		return state, nil
	}
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return State{}, err
	}
	if state.Chats == nil {
		state.Chats = map[string]ChatState{}
	}
	return state, nil
}

func (s Store) Save(state State) error {
	if s.Path == "" {
		return nil
	}
	if state.Chats == nil {
		state.Chats = map[string]ChatState{}
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}
