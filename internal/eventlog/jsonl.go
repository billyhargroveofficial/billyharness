package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultJSONLMaxRecordBytes = 16 * 1024 * 1024
	defaultJSONLDirMode        = 0o700
	defaultJSONLFileMode       = 0o600
)

type JSONLOptions struct {
	MissingOK      bool
	MaxRecordBytes int
}

type JSONLRecord[T any] struct {
	Path     string
	Line     int
	RecordNo int64
	Value    T
}

type CorruptionError struct {
	Path     string
	Line     int
	RecordNo int64
	Kind     string
	Err      error
}

func NewCorruptionError(path string, line int, recordNo int64, kind string, err error) error {
	if err == nil {
		return nil
	}
	return &CorruptionError{
		Path:     path,
		Line:     line,
		RecordNo: recordNo,
		Kind:     kind,
		Err:      err,
	}
}

func (e *CorruptionError) Error() string {
	if e == nil {
		return ""
	}
	loc := e.Path
	switch {
	case e.Path != "" && e.Line > 0 && e.RecordNo > 0 && int64(e.Line) != e.RecordNo:
		loc = fmt.Sprintf("%s:%d record %d", e.Path, e.Line, e.RecordNo)
	case e.Path != "" && e.Line > 0:
		loc = fmt.Sprintf("%s:%d", e.Path, e.Line)
	case e.Path != "" && e.RecordNo > 0:
		loc = fmt.Sprintf("%s record %d", e.Path, e.RecordNo)
	case e.Line > 0:
		loc = fmt.Sprintf("line %d", e.Line)
	case e.RecordNo > 0:
		loc = fmt.Sprintf("record %d", e.RecordNo)
	default:
		loc = "event log"
	}
	if e.Kind != "" {
		return fmt.Sprintf("%s %s: %v", loc, e.Kind, e.Err)
	}
	return fmt.Sprintf("%s %v", loc, e.Err)
}

func (e *CorruptionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func AppendJSONL(path string, value any) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), defaultJSONLDirMode); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, defaultJSONLFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Chmod(defaultJSONLFileMode); err != nil {
		return err
	}
	return nil
}

func ReplayJSONL[T any](path string, opts JSONLOptions, visit func(JSONLRecord[T]) error) error {
	file, err := os.Open(path)
	if err != nil {
		if opts.MissingOK && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	maxRecordBytes := opts.MaxRecordBytes
	if maxRecordBytes <= 0 {
		maxRecordBytes = defaultJSONLMaxRecordBytes
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRecordBytes)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		var value T
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			return NewCorruptionError(path, lineNo, int64(lineNo), "invalid JSONL record", err)
		}
		if visit != nil {
			if err := visit(JSONLRecord[T]{
				Path:     path,
				Line:     lineNo,
				RecordNo: int64(lineNo),
				Value:    value,
			}); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return NewCorruptionError(path, lineNo+1, int64(lineNo+1), "read JSONL record", err)
	}
	return nil
}
