// Package migrations embeds the SQL schema migrations (REQ-DB-003) for both
// dialects and exposes them to the db runner. Files are numbered
// NNNN_name.up-style and applied in lexicographic order.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed sqlite mariadb
var all embed.FS

// For returns the migration files for a dialect ("sqlite" or "mariadb"),
// sorted by name so they apply in order.
func For(dialect string) ([]string, error) {
	entries, err := fs.ReadDir(all, dialect)
	if err != nil {
		return nil, fmt.Errorf("no migrations for dialect %q: %w", dialect, err)
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// Read returns the bytes of a named migration file within a dialect dir.
func Read(dialect, name string) ([]byte, error) {
	b, err := all.ReadFile(dialect + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("read migration %s/%s: %w", dialect, name, err)
	}
	return b, nil
}
