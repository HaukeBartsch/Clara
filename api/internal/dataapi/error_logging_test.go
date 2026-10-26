package dataapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// recordSink collects log records for assertions (slog.FnHandler needs Go 1.24,
// and this module builds on the distribution's Go).
type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *recordSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *recordSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r.Clone())
	return nil
}

func (s *recordSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *recordSink) WithGroup(string) slog.Handler      { return s }

// lines flattens what was logged, message plus the attributes that matter.
func (s *recordSink) lines() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.records))
	for _, r := range s.records {
		line := r.Message
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "err" || a.Key == "at" {
				line += " " + a.Key + "=" + a.Value.String()
			}
			return true
		})
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestStoreFailureIsLoggedButNotDisclosed pins both halves of REQ-API-006: the
// client gets a bare 500 with no internal detail, and the server keeps the
// cause. Before this, the ~40 internal-error sites dropped the Go error on the
// floor, so an intermittent failure left nothing anywhere — diagnosing one took
// per-statement metrics counters instead of a log line.
func TestStoreFailureIsLoggedButNotDisclosed(t *testing.T) {
	f := newRecordFixture(t)

	sink := &recordSink{}
	prev := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Break the store: every query from here on fails.
	if err := f.s.DB.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	code, body := f.call(t, url.Values{
		"token": {"tok-edit"}, "content": {"record"}, "returnFormat": {"json"},
	})

	if code != http.StatusInternalServerError {
		t.Fatalf("export against a closed store = %d %s, want 500", code, body)
	}
	for _, leak := range []string{"sql:", "closed", "sqlite", ".go:"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("response body discloses internal detail (%q): %s", leak, body)
		}
	}

	logged := sink.lines()
	if logged == "" {
		t.Fatal("the 500 produced no log line at all — the cause is discarded again")
	}
	if !strings.Contains(logged, "closed") {
		t.Errorf("no log line carries the cause:\n%s", logged)
	}
	if !strings.Contains(logged, ".go:") {
		t.Errorf("no log line names a call site, so it cannot be traced back:\n%s", logged)
	}
}
