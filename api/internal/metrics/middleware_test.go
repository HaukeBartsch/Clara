package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareCountsRouteStatusAndBytes(t *testing.T) {
	r := New()
	next := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "hello world")
	})
	h := Middleware(r, StaticRoute("/api/"), next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/", strings.NewReader("payload")))

	snap := r.Snapshot()
	if len(snap.HTTP.Routes) != 1 {
		t.Fatalf("routes = %v", snap.HTTP.Routes)
	}
	rs := snap.HTTP.Routes[0]
	if rs.Route != "POST /api/" {
		t.Errorf("route = %q, want \"POST /api/\"", rs.Route)
	}
	if rs.Count != 1 || rs.Status2xx != 1 {
		t.Errorf("count/status = %d/%d, want 1/1", rs.Count, rs.Status2xx)
	}
	if rs.ReqBytesTotal != 7 || rs.RespBytesTotal != 11 {
		t.Errorf("bytes = %d/%d, want 7/11", rs.ReqBytesTotal, rs.RespBytesTotal)
	}
}

func TestMiddlewareCountsChunkedBody(t *testing.T) {
	r := New()
	h := Middleware(r, StaticRoute("/api/"), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body) // the handler must still see the full body
		if string(body) != "chunked payload" {
			t.Errorf("handler saw %q", body)
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/", io.NopCloser(strings.NewReader("chunked payload")))
	req.ContentLength = -1 // force the counting reader
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := r.Snapshot().HTTP.Routes[0].ReqBytesTotal; got != 15 {
		t.Errorf("chunked req_bytes_total = %d, want 15", got)
	}
}

func TestMiddlewareUnknownMethodAndUnmatched(t *testing.T) {
	r := New()
	h := Middleware(r, func(*http.Request) string { return "" }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("TEAPOT", "/x", nil))

	if route := r.Snapshot().HTTP.Routes[0].Route; route != "OTHER unmatched" {
		t.Errorf("route = %q, want \"OTHER unmatched\"", route)
	}
}

func TestMiddlewareFlushPassthrough(t *testing.T) {
	r := New()
	h := Middleware(r, StaticRoute("/api/"), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if f, ok := w.(http.Flusher); !ok {
			t.Error("countWriter must implement Flusher for streamed exports")
		} else {
			f.Flush()
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/", nil))
}

type identityHandler struct{ served bool }

func (h *identityHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) { h.served = true }

func TestMiddlewareNilRegistryPassthrough(t *testing.T) {
	next := &identityHandler{}
	if h := Middleware(nil, nil, next); h != http.Handler(next) {
		t.Error("nil registry must return the next handler unchanged")
	}
}
