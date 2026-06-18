package jobqueue

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "queue.jsonl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func ids(jobs []Job) map[string]Job {
	m := map[string]Job{}
	for _, j := range jobs {
		m[j.ID] = j
	}
	return m
}

func TestQueuedThenLoadPending(t *testing.T) {
	s := open(t)
	jobs := []Job{
		{ID: "1:10", ChatID: 1, StatusID: 10, URL: "https://v.douyin.com/A/"},
		{ID: "1:11", ChatID: 1, StatusID: 11, URL: "https://v.douyin.com/B/"},
		{ID: "2:12", ChatID: 2, StatusID: 12, URL: "https://v.douyin.com/C/"},
	}
	for _, j := range jobs {
		if err := s.MarkQueued(j); err != nil {
			t.Fatalf("MarkQueued: %v", err)
		}
	}
	got, err := s.LoadPending()
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 pending, got %d", len(got))
	}
	m := ids(got)
	if j := m["1:10"]; j.ChatID != 1 || j.StatusID != 10 || j.URL != "https://v.douyin.com/A/" {
		t.Errorf("job 1:10 fields wrong: %+v", j)
	}
}

func TestTerminalExcludesFromPending(t *testing.T) {
	s := open(t)
	for _, j := range []Job{
		{ID: "1:10", ChatID: 1, StatusID: 10, URL: "a"},
		{ID: "1:11", ChatID: 1, StatusID: 11, URL: "b"},
		{ID: "1:12", ChatID: 1, StatusID: 12, URL: "c"},
	} {
		s.MarkQueued(j)
	}
	if err := s.MarkDone("1:11"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if err := s.MarkFailed("1:12"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, _ := s.LoadPending()
	if len(got) != 1 || got[0].ID != "1:10" {
		t.Fatalf("want only 1:10 pending, got %+v", got)
	}
}

func TestCompactKeepsOnlyPending(t *testing.T) {
	s := open(t)
	for _, j := range []Job{
		{ID: "1:10", ChatID: 1, StatusID: 10, URL: "a"},
		{ID: "1:11", ChatID: 1, StatusID: 11, URL: "b"},
	} {
		s.MarkQueued(j)
	}
	s.MarkDone("1:11")
	pending, _ := s.LoadPending()
	if err := s.Compact(pending); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got, _ := s.LoadPending()
	if len(got) != 1 || got[0].ID != "1:10" {
		t.Fatalf("after compact want only 1:10, got %+v", got)
	}
	// 新任务仍可继续追加并被读到
	s.MarkQueued(Job{ID: "1:13", ChatID: 1, StatusID: 13, URL: "d"})
	got, _ = s.LoadPending()
	if len(got) != 2 {
		t.Fatalf("after append want 2 pending, got %d", len(got))
	}
}

func TestLoadPendingMissingFile(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := s.LoadPending()
	if err != nil {
		t.Fatalf("LoadPending on missing file should be nil err, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 pending, got %d", len(got))
	}
}
