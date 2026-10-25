package admin

import (
	"fmt"
	"regexp"
	"strings"
)

const maxInstrumentNameLength = 255

var invalidInstrumentNameChars = regexp.MustCompile(`[^A-Za-z0-9_ ]+`)

func validInstrumentName(name string) bool {
	return len(name) > 0 && len(name) <= maxInstrumentNameLength &&
		strings.TrimSpace(name) != "" && !invalidInstrumentNameChars.MatchString(name)
}

func importedInstrumentName(name string, position int) (string, bool) {
	if validInstrumentName(name) {
		return name, false
	}
	original := strings.TrimSpace(name)
	candidate := invalidInstrumentNameChars.ReplaceAllString(original, "_")
	if candidate == "" {
		candidate = fmt.Sprintf("instrument_%d", position)
	}
	if len(candidate) > maxInstrumentNameLength {
		candidate = candidate[:maxInstrumentNameLength]
	}
	return candidate, candidate != name
}

func numberedInstrumentName(name string, n int) string {
	suffix := fmt.Sprintf("_%d", n)
	if len(name)+len(suffix) > maxInstrumentNameLength {
		name = name[:maxInstrumentNameLength-len(suffix)]
	}
	return name + suffix
}
