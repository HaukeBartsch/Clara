package metrics

import (
	"io"
	"net/http"
	"time"
)

// Middleware times every request that reaches it and observes it into reg
// (REQ-API-163). A nil registry returns next untouched — zero overhead for
// uninstrumented servers and test binaries.
//
// route resolves the low-cardinality path template for the request (a server
// pattern, never a concrete path value); nil or an empty answer observes as
// "unmatched". The response status and bytes come from a wrapping writer
// (Flush passes through so streamed exports keep streaming); the request byte
// count is ContentLength when known, else counted off the body — the counting
// reader is attached only for chunked bodies of methods that may carry one.
func Middleware(reg *Registry, route func(*http.Request) string, next http.Handler) http.Handler {
	if reg == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var cr *countReader
		if r.Body != nil && r.ContentLength < 0 && mayHaveBody(r.Method) {
			cr = &countReader{ReadCloser: r.Body}
			r.Body = cr
		}

		cw := &countWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)

		reqBytes := r.ContentLength
		if cr != nil {
			reqBytes = cr.n
		}
		var pattern string
		if route != nil {
			pattern = route(r)
		}
		reg.ObserveHTTP(pattern, r.Method, cw.status, time.Since(start), reqBytes, cw.n)
	})
}

// StaticRoute is the resolver for a fixed mount: every request under it shares
// one label (the data API dispatches by parameter, so its paths are already
// low-cardinality).
func StaticRoute(pattern string) func(*http.Request) string {
	return func(*http.Request) string { return pattern }
}

func mayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// countWriter records the response status and the bytes written. Unwrap keeps
// http.ResponseController working on the underlying writer.
type countWriter struct {
	http.ResponseWriter
	status int
	n      int64
}

func (w *countWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *countWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}

func (w *countWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *countWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// countReader counts the bytes read from a request body of unknown length.
type countReader struct {
	io.ReadCloser
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}
