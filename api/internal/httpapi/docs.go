package httpapi

import (
	"net/http"

	"csms/api/docs"
	"csms/api/openapi"
)

// Documentation endpoints (API_Endpoints_Design.md §2.2,
// Technology_Stack_Design.md §3/§5): the embedded OpenAPI 3.1 document at
// GET /openapi.json and an interactive Swagger UI at GET /docs backed by the
// vendored swagger-ui-dist bundle under /docs/swaggerui/. Like /healthz they
// carry no authentication in Go — nginx keeps /docs and /openapi.json off the
// public network together with /api/v1/*.

func registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /openapi.json", openAPIJSONHandler)
	mux.HandleFunc("GET /docs", docsUIHandler)
	mux.Handle("/docs/", http.StripPrefix("/docs", docsAssetHandler()))
}

// openAPIJSONHandler serves the embedded spec verbatim.
func openAPIJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openapi.Spec)
}

// docsUIPage is the Swagger UI host page. The bundle is loaded from the
// vendored assets (never a CDN, REQ-TECH-021) and pointed at the embedded
// spec. Try-it-out is enabled so an internal reviewer can supply the
// X-Internal-Service-Token / X-Internal-User-Id headers through Authorize;
// nothing is persisted in the browser.
const docsUIPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CLARA API</title>
<link rel="stylesheet" href="/docs/swaggerui/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="/docs/swaggerui/swagger-ui-bundle.js"></script>
<script>
window.ui = SwaggerUIBundle({
  dom_id: '#swagger-ui',
  url: '/openapi.json',
  docExpansion: 'list',
  tryItOutEnabled: true,
  persistAuthorization: false,
});
</script>
</body>
</html>
`

func docsUIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsUIPage))
}

// docsAssetHandler serves the embedded bundle under /docs/ (so
// /docs/swaggerui/swagger-ui.css and swagger-ui-bundle.js resolve); a request
// for the subtree root shows the UI page as well, so /docs/ works like /docs.
func docsAssetHandler() http.Handler {
	files := http.FileServerFS(docs.FS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || r.URL.Path == "/" {
			docsUIHandler(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
