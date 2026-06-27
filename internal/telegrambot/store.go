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
	return os.WriteFile(s.Path, append(body, '\n'), 0o600)
}
