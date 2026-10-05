// Package openapi embeds the hand-maintained OpenAPI 3.1 document for both
// API surfaces (REQ-API-002, Technology_Stack_Design.md §3). The document is
// edited directly at api/openapi/openapi.json — no code generation — and is
// embedded so the release stays a single static binary (REQ-TECH-013).
package openapi

import _ "embed"

// Spec is the OpenAPI 3.1 document, served verbatim at GET /openapi.json
// (API_Endpoints_Design.md §2.2).
//
//go:embed openapi.json
var Spec []byte
