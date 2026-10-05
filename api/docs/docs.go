// Package docs embeds the vendored Swagger UI bundle (swagger-ui-dist:
// swagger-ui.css and swagger-ui-bundle.js under swaggerui/). The assets are
// vendored per REQ-TECH-021 — never loaded from a CDN at runtime — and
// embedded so the release stays a single static binary (REQ-TECH-013); they
// are served under /docs/swaggerui/ by the httpapi package.
package docs

import "embed"

// FS is the embedded asset tree rooted at the package directory; the bundle
// lives at swaggerui/.
//
//go:embed swaggerui
var FS embed.FS
