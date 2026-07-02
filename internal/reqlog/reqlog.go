// Package reqlog persists a record of each /api/process call (timestamp,
// file kind, summary, and the full raw moderation response) to a local
// logs directory as one JSON file per call, plus a rolling index file
// that the frontend can fetch to list recent calls.
package reqlog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one logged API call result.
type Entry struct {
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Kind      string      `json:"kind"` // "image" or "video"
	Filename  string      `json:"filename,omitempty"`
	Status    string      `json:"status"` // "ok" or "error"
	Summary   interface{} `json:"summary,omitempty"`
	Raw       interface{} `json:"raw,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// Logger writes Entry records to a directory on disk. Safe for concurrent
// use by multiple goroutines/requests.
type Logger struct {
	dir string
	mu  sync.Mutex // serializes index file read-modify-write
}

// New creates a Logger writing to dir, creating the directory if needed.
func New(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("reqlog: create logs dir: %w", err)
	}
	return &Logger{dir: dir}, nil
}

// indexEntry is the lightweight summary stored in the rolling index, so
// the frontend can list recent calls without fetching every full record.
type indexEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`
	Filename  string    `json:"filename,omitempty"`
	Status    string    `json:"status"`
}

const (
	indexFilename = "index.json"
	maxIndexSize  = 200 // keep the most recent N entries in the index
)

// Log writes entry to its own JSON file and appends a lightweight record
// to the rolling index. Errors are returned but are non-fatal to the
// caller's actual request handling — callers should log-and-continue
// rather than fail the API response if this fails.
func (l *Logger) Log(entry Entry) error {
	if entry.ID == "" {
		id, err := randomID()
		if err != nil {
			return fmt.Errorf("reqlog: generate id: %w", err)
		}
		entry.ID = id
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	filename := fmt.Sprintf("%s_%s.json", entry.Timestamp.Format("20060102T150405Z"), entry.ID)
	fullPath := filepath.Join(l.dir, filename)

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("reqlog: marshal entry: %w", err)
	}
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return fmt.Errorf("reqlog: write entry file: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.appendIndex(indexEntry{
		ID:        entry.ID,
		Timestamp: entry.Timestamp,
		Kind:      entry.Kind,
		Filename:  entry.Filename,
		Status:    entry.Status,
	}, filename)
}

type indexRecord struct {
	indexEntry
	File string `json:"file"`
}

// appendIndex adds a record to the rolling index, trimming to maxIndexSize
// most-recent entries. Must be called with l.mu held.
func (l *Logger) appendIndex(e indexEntry, file string) error {
	indexPath := filepath.Join(l.dir, indexFilename)

	var records []indexRecord
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &records) // best-effort; start fresh on corruption
	}

	records = append(records, indexRecord{indexEntry: e, File: file})

	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})
	if len(records) > maxIndexSize {
		records = records[:maxIndexSize]
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("reqlog: marshal index: %w", err)
	}
	return os.WriteFile(indexPath, data, 0o644)
}

// List returns the current rolling index (most recent first).
func (l *Logger) List() ([]indexRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	indexPath := filepath.Join(l.dir, indexFilename)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []indexRecord{}, nil
		}
		return nil, fmt.Errorf("reqlog: read index: %w", err)
	}

	var records []indexRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("reqlog: parse index: %w", err)
	}
	return records, nil
}

// Get reads back a single full log entry by its index-listed filename.
// filename is validated to prevent path traversal outside the logs dir.
func (l *Logger) Get(filename string) ([]byte, error) {
	if strings.Contains(filename, "..") || strings.ContainsAny(filename, "/\\") {
		return nil, fmt.Errorf("reqlog: invalid filename %q", filename)
	}
	return os.ReadFile(filepath.Join(l.dir, filename))
}

func randomID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
