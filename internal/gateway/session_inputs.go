package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/eventlog"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
)

const (
	sessionInputAdmitted  = "admitted"
	sessionInputPromoted  = "promoted"
	sessionInputCompleted = "completed"
	sessionInputAmbiguous = "ambiguous"
)

type sessionInputRecord struct {
	SchemaVersion  int                            `json:"schema_version"`
	Seq            int64                          `json:"seq"`
	SessionID      string                         `json:"session_id"`
	Timestamp      time.Time                      `json:"ts"`
	Kind           string                         `json:"kind"`
	InputID        string                         `json:"input_id"`
	Prompt         string                         `json:"prompt,omitempty"`
	BodySHA256     string                         `json:"body_sha256,omitempty"`
	RunSeq         int64                          `json:"run_seq,omitempty"`
	TerminalStatus string                         `json:"terminal_status,omitempty"`
	Request        gatewayapi.SessionInputRequest `json:"request,omitempty"`
}

type sessionInputState struct {
	InputID        string
	Prompt         string
	BodySHA256     string
	State          string
	RunSeq         int64
	TerminalStatus string
	LastSeq        int64
}

type replayedSessionInputs struct {
	lastSeq int64
	inputs  map[string]sessionInputState
}

type sessionInputConflictError struct {
	inputID string
}

func (e *sessionInputConflictError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("input_id %q already exists with different body", e.inputID)
}

func (s *sessionStore) AdmitInput(session *Session, req gatewayapi.SessionInputRequest) (gatewayapi.SessionInputResponse, error) {
	if strings.TrimSpace(req.InputID) == "" {
		req.InputID = newID()
	}
	req = normalizeSessionInputRequest(req)
	inputID, err := cleanSessionInputID(req.InputID)
	if err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return gatewayapi.SessionInputResponse{}, fmt.Errorf("prompt required")
	}
	req.InputID = inputID
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return gatewayapi.SessionInputResponse{InputID: inputID, State: sessionInputAdmitted}, nil
	}
	bodySHA, err := hashSessionInputRequest(req)
	if err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	id, inputsPath, err := s.sessionInputsPathLocked(session)
	if err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	replayed, err := replaySessionInputs(inputsPath, id)
	if err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	if existing, ok := replayed.inputs[inputID]; ok {
		if existing.BodySHA256 != bodySHA {
			return gatewayapi.SessionInputResponse{}, &sessionInputConflictError{inputID: inputID}
		}
		return gatewayapi.SessionInputResponse{
			InputID:   inputID,
			State:     existing.State,
			Duplicate: true,
			Seq:       existing.LastSeq,
		}, nil
	}
	record := sessionInputRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           replayed.lastSeq + 1,
		SessionID:     id,
		Timestamp:     time.Now().UTC(),
		Kind:          sessionInputAdmitted,
		InputID:       inputID,
		Prompt:        req.Prompt,
		BodySHA256:    bodySHA,
		Request:       req,
	}
	if err := eventlog.AppendJSONL(inputsPath, record); err != nil {
		return gatewayapi.SessionInputResponse{}, err
	}
	return gatewayapi.SessionInputResponse{InputID: inputID, State: sessionInputAdmitted, Seq: record.Seq}, nil
}

func (s *sessionStore) PromoteInput(session *Session, inputID string, runSeq int64) error {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return nil
	}
	inputID, err := cleanSessionInputID(inputID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, inputsPath, err := s.sessionInputsPathLocked(session)
	if err != nil {
		return err
	}
	replayed, err := replaySessionInputs(inputsPath, id)
	if err != nil {
		return err
	}
	state, ok := replayed.inputs[inputID]
	if !ok {
		return fmt.Errorf("input_id %q was not admitted", inputID)
	}
	if state.State != sessionInputAdmitted {
		return fmt.Errorf("input_id %q is already %s", inputID, state.State)
	}
	record := sessionInputRecord{
		SchemaVersion: gatewaySessionSchemaVersion,
		Seq:           replayed.lastSeq + 1,
		SessionID:     id,
		Timestamp:     time.Now().UTC(),
		Kind:          sessionInputPromoted,
		InputID:       inputID,
		BodySHA256:    state.BodySHA256,
		RunSeq:        runSeq,
	}
	return eventlog.AppendJSONL(inputsPath, record)
}

func (s *sessionStore) CompleteInput(session *Session, inputID string, runSeq int64, terminalStatus string) error {
	if s == nil || strings.TrimSpace(s.dir) == "" || session == nil {
		return nil
	}
	inputID, err := cleanSessionInputID(inputID)
	if err != nil {
		return err
	}
	terminalStatus = strings.TrimSpace(terminalStatus)
	if terminalStatus == "" {
		terminalStatus = "completed"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, inputsPath, err := s.sessionInputsPathLocked(session)
	if err != nil {
		return err
	}
	replayed, err := replaySessionInputs(inputsPath, id)
	if err != nil {
		return err
	}
	state, ok := replayed.inputs[inputID]
	if !ok {
		return fmt.Errorf("input_id %q was not admitted", inputID)
	}
	if state.State == sessionInputCompleted {
		return nil
	}
	record := sessionInputRecord{
		SchemaVersion:  gatewaySessionSchemaVersion,
		Seq:            replayed.lastSeq + 1,
		SessionID:      id,
		Timestamp:      time.Now().UTC(),
		Kind:           sessionInputCompleted,
		InputID:        inputID,
		BodySHA256:     state.BodySHA256,
		RunSeq:         runSeq,
		TerminalStatus: terminalStatus,
	}
	return eventlog.AppendJSONL(inputsPath, record)
}

func (s *sessionStore) sessionInputsPathLocked(session *Session) (string, string, error) {
	id, err := cleanSessionID(session.ID)
	if err != nil {
		return "", "", err
	}
	if err := ensurePrivateGatewayDir(s.dir); err != nil {
		return "", "", err
	}
	sessionDir := filepath.Join(s.dir, id)
	if err := ensurePrivateGatewayDir(sessionDir); err != nil {
		return "", "", err
	}
	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	manifest, err := readSessionManifest(manifestPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		if err := s.saveLocked(session); err != nil {
			return "", "", err
		}
		manifest, err = readSessionManifest(manifestPath)
		if err != nil {
			return "", "", err
		}
	}
	if strings.TrimSpace(manifest.InputsJSONL) == "" {
		manifest.InputsJSONL = sessionInputsJSONLName
		if err := writeSessionManifest(manifestPath, manifest); err != nil {
			return "", "", err
		}
	}
	return id, filepath.Join(sessionDir, sessionFileName(manifest.InputsJSONL, sessionInputsJSONLName)), nil
}

func markPromotedSessionInputsAmbiguous(path, sessionID string) error {
	replayed, err := replaySessionInputs(path, sessionID)
	if err != nil {
		return err
	}
	nextSeq := replayed.lastSeq
	for _, state := range replayed.inputs {
		if state.State != sessionInputPromoted {
			continue
		}
		nextSeq++
		record := sessionInputRecord{
			SchemaVersion:  gatewaySessionSchemaVersion,
			Seq:            nextSeq,
			SessionID:      sessionID,
			Timestamp:      time.Now().UTC(),
			Kind:           sessionInputAmbiguous,
			InputID:        state.InputID,
			BodySHA256:     state.BodySHA256,
			RunSeq:         state.RunSeq,
			TerminalStatus: "ambiguous_after_restart",
		}
		if err := eventlog.AppendJSONL(path, record); err != nil {
			return err
		}
	}
	return nil
}

func replaySessionInputs(path, sessionID string) (replayedSessionInputs, error) {
	out := replayedSessionInputs{inputs: map[string]sessionInputState{}}
	expectedSeq := int64(1)
	err := eventlog.ReplayJSONL[sessionInputRecord](path, eventlog.JSONLOptions{MissingOK: true}, func(item eventlog.JSONLRecord[sessionInputRecord]) error {
		record := item.Value
		recordNo := expectedSeq
		if record.SchemaVersion != 0 && record.SchemaVersion != gatewaySessionSchemaVersion {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported schema_version %d", record.SchemaVersion))
		}
		if record.Seq != expectedSeq {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("sequence gap: got %d want %d", record.Seq, expectedSeq))
		}
		if record.SessionID != sessionID {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("session_id = %q, want %q", record.SessionID, sessionID))
		}
		inputID, err := cleanSessionInputID(record.InputID)
		if err != nil {
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", err)
		}
		state := out.inputs[inputID]
		switch record.Kind {
		case sessionInputAdmitted:
			if state.InputID != "" {
				return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("duplicate admission for input_id %q", inputID))
			}
			if record.BodySHA256 == "" {
				return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("missing body_sha256 for input_id %q", inputID))
			}
			state = sessionInputState{
				InputID:    inputID,
				Prompt:     record.Prompt,
				BodySHA256: record.BodySHA256,
				State:      sessionInputAdmitted,
			}
		case sessionInputPromoted:
			if state.InputID == "" {
				return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("promotion without admission for input_id %q", inputID))
			}
			state.State = sessionInputPromoted
			state.RunSeq = record.RunSeq
		case sessionInputCompleted:
			if state.InputID == "" {
				return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("completion without admission for input_id %q", inputID))
			}
			state.State = sessionInputCompleted
			state.RunSeq = record.RunSeq
			state.TerminalStatus = record.TerminalStatus
		case sessionInputAmbiguous:
			if state.InputID == "" {
				return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("ambiguity without admission for input_id %q", inputID))
			}
			state.State = sessionInputAmbiguous
			state.RunSeq = record.RunSeq
			state.TerminalStatus = record.TerminalStatus
		default:
			return eventlog.NewCorruptionError(path, item.Line, recordNo, "", fmt.Errorf("unsupported kind %q", record.Kind))
		}
		state.LastSeq = record.Seq
		out.inputs[inputID] = state
		out.lastSeq = record.Seq
		expectedSeq++
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

func normalizeSessionInputRequest(req gatewayapi.SessionInputRequest) gatewayapi.SessionInputRequest {
	req.InputID = strings.TrimSpace(req.InputID)
	req.InterruptPolicy = strings.ToLower(strings.TrimSpace(req.InterruptPolicy))
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.ClientType = strings.TrimSpace(req.ClientType)
	if len(req.Metadata) == 0 {
		req.Metadata = nil
	}
	return req
}

func hashSessionInputRequest(req gatewayapi.SessionInputRequest) (string, error) {
	bytes, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func cleanSessionInputID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("input_id required")
	}
	if id == "." || id == ".." || strings.Contains(id, "/") || strings.Contains(id, "\\") || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid input_id %q", id)
	}
	return id, nil
}
