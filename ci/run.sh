#!/usr/bin/env bash
# CI pipeline (Design/Technology_Stack_Design.md §4 and §6): go vet + unit +
# integration tests against a temporary SQLite file with no external services
# (REQ-TECH-021), plus the Fiona compatibility fixtures — the table-driven
# encodings of the call examples in VISION_AND_REQUIREMENTS.md
# ("Endpoints used by Fiona"), charter success criterion 1 as an executable
# regression test (REQ-TECH-022, REQ-API-037).
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root/api"

echo "==> go vet"
go vet ./...

echo "==> unit + integration tests (SQLite, no external services)"
go test ./...

echo "==> Fiona compatibility fixtures (REQ-API-037)"
# Fixture convention: tests named TestFiona* in the Go tree. When they are
# absent the run is NOT green — success criterion 1 stays uncovered until the
# examples are encoded against a seeded project.
if grep -rqE '^func Test[A-Za-z0-9_]*Fiona' . --include='*_test.go'; then
	go test ./... -run 'Fiona' -v
else
	echo "FAIL: no Fiona fixture tests (TestFiona*) found — REQ-API-037 uncovered." >&2
	exit 1
fi
