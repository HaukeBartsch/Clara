package admin

// Project interchange in CDISC ODM XML (REQ-ODM-…, Design/ODM_Interchange_Design.md):
// the pieces both directions share — the dialect constants, the OID scheme (O-1),
// the problem-report rows (§7) and the pre-parse refusals of REQ-ODM-013.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// odmNamespace is ODM 1.3's — 1.3.2 documents share it, which is why the
	// producer targets 1.3.2 element usage inside it (REQ-ODM-010, DEV-ODM-5).
	odmNamespace = "http://www.cdisc.org/ns/odm/v1.3"

	// odmVersion is written into ODMVersion; readers built for 1.3.2 accept the
	// document (REQ-ODM-010).
	odmVersion = "1.3.2"

	odm20Namespace = "http://www.cdisc.org/ns/odm/v2.0"
	odm20Version   = "2.0"

	// claraExtNamespace carries what ODM has no element for — branching logic,
	// the sensitivity flags, transfer instructions (§5, DEV-ODM-3). A conforming
	// reader ignores it; a missing one means CLARA's own defaults (REQ-ODM-052).
	claraExtNamespace = "urn:x-clara:odm-ext:v1"

	// odmMaxDepth bounds element nesting so a hostile document costs one request
	// rather than a stack (REQ-ODM-014, O-8).
	odmMaxDepth = 64

	// odmOIDLimit keeps an OID inside the schema's 256 characters with room for
	// the prefix; a longer name is cut and salted with its own digest tail so two
	// long names never collide (O-1).
	odmOIDLimit = 200
)

type odmDialect struct {
	selection string
	namespace string
	version   string
}

func odmDialectForVersion(version string) (odmDialect, bool) {
	switch version {
	case "", "1.3":
		return odmDialect{selection: "1.3", namespace: odmNamespace, version: odmVersion}, true
	case "2.0":
		return odmDialect{selection: "2.0", namespace: odm20Namespace, version: odm20Version}, true
	default:
		return odmDialect{}, false
	}
}

// isODM13 reports the dialect's vintage where the two schemas disagree about
// vocabulary, which is twice in this exporter:
//
//   - clinical data: 1.3.2 lets StudyEventData hold only AuditRecord, Signature,
//     Annotation and FormData, so a clinical ItemGroupData has no other parent; 2.0
//     removed the level and hangs groups directly off StudyEventData (REQ-ODM-027);
//   - the per-pair repeating flag: 2.0 carries it as FormRef/@Repeating, while
//     1.3.2's FormRef takes only FormOID plus the shared Mandatory/OrderNumber
//     (REQ-ODM-029).
func (d odmDialect) isODM13() bool { return d.namespace == odmNamespace }

// odmSASPattern is ODM's own sasName pattern, and eight characters is its own
// length cap (ODM1-3-2-foundation.xsd: maxLength 8 plus this expression).
var odmSASPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// odmSASName reports whether a CLARA name may be written as an ODM SAS name.
// CLARA names routinely run past eight characters and may hold spaces, so the ones
// that do not fit are left out rather than shortened: an eight-character truncation
// collides between fields, cannot round-trip, and misrepresents the machine name
// @Name already carries (REQ-ODM-028). Import reads @Name and never these.
func odmSASName(name string) (string, bool) {
	if len(name) > 8 || !odmSASPattern.MatchString(name) {
		return "", false
	}
	return name, true
}

// odmLocalName maps a CLARA name onto the character set an OID accepts. CLARA
// field names are already lower-case alnum plus underscore, so this is the
// identity for everything this system created and a repair for names that came
// from elsewhere (an event label with punctuation).
var odmLocalName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// odmOID builds one object's OID from its kind prefix and CLARA name (O-1):
// two exports of the same project agree, so a partner's mappings survive a
// reload. The original survives in the extension when this rewrote it.
func odmOID(prefix, name string) string {
	sanitized := strings.Trim(odmLocalName.ReplaceAllString(name, "_"), "._-")
	if sanitized == "" {
		sanitized = "x"
	}
	oid := prefix + sanitized
	if len(oid) <= odmOIDLimit {
		return oid
	}
	sum := sha256.Sum256([]byte(name))
	cut := odmOIDLimit - 1 - hex.EncodedLen(4)
	return oid[:cut] + "_" + hex.EncodeToString(sum[:4])
}

// odmProblem is one line of the import report (REQ-ODM-060) — the instrument
// import's importNote shape, so a caller branches on `code` across both.
type odmProblem struct {
	File    string `json:"file"`
	Row     string `json:"row,omitempty"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// odmProblems collects the report's lines with a cap, so a document that fails
// on every value cannot make the answer larger than the request it answers.
type odmProblems struct {
	list   []odmProblem
	capped int
}

const odmProblemCap = 200

func (p *odmProblems) add(row, field, code, message string) {
	if len(p.list) >= odmProblemCap {
		p.capped++
		return
	}
	p.list = append(p.list, odmProblem{File: "project.xml", Row: row, Field: field, Code: code, Message: message})
}

// fatalRow records one problem that stops the import and returns the error that
// unwinds the resolution. The caller answers 400 with the list and writes nothing
// (REQ-ODM-054).
func (p *odmProblems) fatalRow(row, field, code, message string) error {
	p.add(row, field, code, message)
	return errODMFatal
}

// out is the report's problem list: empty rather than null, so a caller can
// iterate without a nil check.
func (p *odmProblems) out() []odmProblem {
	out := p.list
	if out == nil {
		out = []odmProblem{}
	}
	if p.capped > 0 {
		out = append(out, odmProblem{
			File: "project.xml", Code: "problems_truncated",
			Message: fmt.Sprintf("%d further problems are not listed", p.capped),
		})
	}
	return out
}

// odmFatal marks a document that cannot build at all (REQ-ODM-054): the codes
// the design document's §7 table lists as fatal, plus `validation_error` — the
// instrument import's own code for a design rule one field breaks, fatal here
// because a structure attribute that cannot be represented fails the whole
// structure rather than a single row (REQ-ODM-042).
var odmFatal = map[string]bool{
	"not_odm": true, "dtd_forbidden": true, "too_deep": true, "too_large": true,
	"duplicate_oid": true, "dangling_ref": true, "empty_itemgroup": true, "no_metadataversion": true,
	"validation_error": true,
}

func (p *odmProblems) failed() bool {
	for _, problem := range p.list {
		if odmFatal[problem.Code] {
			return true
		}
	}
	return false
}

// odmCheckBody applies the refusals that must happen before a parser is trusted
// with the bytes: UTF-8 (a BOM tolerated, as the CSV reader tolerates it) and no
// DOCTYPE or entity declaration (REQ-ODM-013). encoding/xml does not expand
// general entities, and refusing anyway keeps a future parser change from
// turning an interchange reader into an expansion path.
func odmCheckBody(body []byte) *odmProblem {
	text := strings.TrimPrefix(string(body), "\uFEFF") // a leading byte-order mark is tolerated, as the CSV reader tolerates one
	if !utf8.ValidString(text) {
		return &odmProblem{File: "project.xml", Code: "not_odm", Message: "the document is not valid UTF-8"}
	}
	head := text
	if len(head) > 4096 {
		head = head[:4096] // a declaration precedes the root element; scanning all of it is pointless
	}
	for _, token := range []string{"<!DOCTYPE", "<!ENTITY", "<!dtd"} {
		if containsFoldASCII(head, token) {
			return &odmProblem{File: "project.xml", Code: "dtd_forbidden", Message: "an ODM document must not declare a DOCTYPE or entities"}
		}
	}
	return nil
}

// containsFoldASCII is strings.Contains for the ASCII case-fold, so `<!Doctype`
// is refused as well as `<!DOCTYPE`.
func containsFoldASCII(haystack, needle string) bool {
	return strings.Contains(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - 32
		}
		return r
	}, haystack), strings.ToUpper(needle))
}
