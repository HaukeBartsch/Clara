package admin

import (
	"strings"
	"testing"
)

func TestImportedInstrumentName(t *testing.T) {
	tests := []struct {
		name     string
		position int
		want     string
		renamed  bool
	}{
		{name: " Incoming Studies_2026 ", position: 1, want: " Incoming Studies_2026 ", renamed: false},
		{name: "incoming-studies", position: 2, want: "incoming_studies", renamed: true},
		{name: " ", position: 3, want: "instrument_3", renamed: true},
		{name: strings.Repeat("A", maxInstrumentNameLength+1), position: 4, want: strings.Repeat("A", maxInstrumentNameLength), renamed: true},
	}
	for _, tt := range tests {
		got, renamed := importedInstrumentName(tt.name, tt.position)
		if got != tt.want || renamed != tt.renamed {
			t.Errorf("importedInstrumentName(%q, %d) = (%q, %v), want (%q, %v)",
				tt.name, tt.position, got, renamed, tt.want, tt.renamed)
		}
	}
}
