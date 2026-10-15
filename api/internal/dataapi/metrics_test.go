package dataapi

import (
	"net/url"
	"testing"

	"csms/api/internal/metrics"
)

// One observation per dispatched call keyed by the closed content vocabulary
// (REQ-API-163): known values pass through, unknown and missing ones fold
// into "other" so a caller string never mints a series.
func TestServeHTTPObservesContentVocabulary(t *testing.T) {
	h, full, _ := testHandler(t)
	reg := metrics.New()
	h.Metrics = reg

	for _, content := range []string{"project", "project", "bogus; DROP TABLE x"} {
		do(t, h, url.Values{"token": {full}, "content": {content}})
	}
	// Missing content answers 400 but was dispatched, so it observes too.
	do(t, h, url.Values{"token": {full}})

	calls := map[string]metrics.ContentStats{}
	for _, c := range reg.Snapshot().DataAPI.Calls {
		calls[c.Content] = c
	}
	if calls["project"].Count != 2 {
		t.Errorf("project count = %d, want 2", calls["project"].Count)
	}
	if calls["other"].Count != 2 {
		t.Errorf("other count = %d, want 2 (unknown + missing content)", calls["other"].Count)
	}
	if len(calls) != 2 {
		t.Errorf("caller string escaped the vocabulary: %v", calls)
	}
}
