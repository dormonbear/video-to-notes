// Package jobqueue persists the bot's task queue to an append-only JSONL event
// log so pending/in-flight jobs survive process restarts. Each state change is
// one appended line; LoadPending replays the log to find jobs that were queued
// but never reached a terminal (done/failed) event.
package jobqueue

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Event string

const (
	Queued Event = "queued"
	Done   Event = "done"
	Failed Event = "failed"
)

// Record is one line in the log.
type Record struct {
	ID       string `json:"id"`
	Event    Event  `json:"event"`
	ChatID   int64  `json:"chat_id,omitempty"`
	StatusID int    `json:"status_id,omitempty"`
	URL      string `json:"url,omitempty"`
	Kind     string `json:"kind,omitempty"` // douyin | twitter | web ("" = legacy douyin)
}

// Job is a unit of work recovered from the log.
type Job struct {
	ID       string
	ChatID   int64
	StatusID int
	URL      string
	Kind     string
}

type Store struct {
	path string
	mu   sync.Mutex
}

// Open ensures the parent directory exists and returns a Store for path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

// Append writes one record as a JSON line. Safe for concurrent callers.
func (s *Store) Append(r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *Store) MarkQueued(j Job) error {
	return s.Append(Record{ID: j.ID, Event: Queued, ChatID: j.ChatID, StatusID: j.StatusID, URL: j.URL, Kind: j.Kind})
}

func (s *Store) MarkDone(id string) error   { return s.Append(Record{ID: id, Event: Done}) }
func (s *Store) MarkFailed(id string) error { return s.Append(Record{ID: id, Event: Failed}) }

// LoadPending replays the log and returns jobs that have a queued event but no
// terminal event, in first-queued order. A missing file yields an empty slice.
func (s *Store) LoadPending() ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	pending := map[string]Job{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // 容纳长 URL 行
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if json.Unmarshal(line, &r) != nil {
			continue // 跳过损坏行（崩溃可能留下半行）
		}
		switch r.Event {
		case Queued:
			if _, ok := pending[r.ID]; !ok {
				order = append(order, r.ID)
			}
			pending[r.ID] = Job{ID: r.ID, ChatID: r.ChatID, StatusID: r.StatusID, URL: r.URL, Kind: r.Kind}
		case Done, Failed:
			delete(pending, r.ID)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]Job, 0, len(pending))
	for _, id := range order {
		if j, ok := pending[id]; ok {
			out = append(out, j)
		}
	}
	return out, nil
}

// Compact rewrites the log to contain only the given pending jobs (one queued
// line each), atomically via a temp file + rename. Keeps the log from growing.
func (s *Store) Compact(pending []Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, j := range pending {
		line, err := json.Marshal(Record{ID: j.ID, Event: Queued, ChatID: j.ChatID, StatusID: j.StatusID, URL: j.URL, Kind: j.Kind})
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
