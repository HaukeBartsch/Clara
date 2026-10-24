package admin

// The ODM export's own contract (REQ-API-169, Design/ODM_Interchange_Design.md §2):
// an installation administrator downloads the project as one well-formed ODM 1.3
// document; anyone else gets the uniform 403 and nothing is audited (REQ-AUD-004).

import (
	"encoding/xml"

	"csms/api/internal/audit"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestExportODMShape(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-admin@example.org")
	projectID := e.mustProject("Interchange")

	rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/export/odm", nil, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "_odm_") || !strings.HasSuffix(cd, ".xml\"") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	body := rec.Body.String()
	if strings.HasPrefix(body, "\uFEFF") {
		t.Error("the document opens with a byte-order mark (REQ-EXP-016)")
	}

	if !strings.HasPrefix(body, `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("document does not open with the XML declaration: %.60s", body)
	}

	var doc struct {
		XMLName          xml.Name `xml:"ODM"`
		Namespace        string   `xml:"xmlns,attr"`
		FileType         string   `xml:"FileType,attr"`
		ODMVersion       string   `xml:"ODMVersion,attr"`
		SourceSystem     string   `xml:"SourceSystem,attr"`
		CreationDateTime string   `xml:"CreationDateTime,attr"`
		Study            struct {
			OID        string `xml:"OID,attr"`
			GlobalVars struct {
				StudyName string `xml:"StudyName"`
			} `xml:"GlobalVariables"`
			MetaDataVersion struct {
				OID string `xml:"OID,attr"`
			} `xml:"MetaDataVersion"`
		} `xml:"Study"`
		ClinicalData any `xml:"ClinicalData"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n---\n%.700s\n---", err, body)
	}
	if doc.XMLName.Local != "ODM" || doc.Namespace != odmNamespace {
		t.Errorf("root = %s in %q, want ODM in %s", doc.XMLName.Local, doc.Namespace, odmNamespace)
	}
	if doc.FileType != "Snapshot" || doc.ODMVersion != odmVersion || doc.SourceSystem != "CLARA" {
		t.Errorf("envelope = %+v/%q/%q", doc.FileType, doc.ODMVersion, doc.SourceSystem)
	}
	if !strings.HasSuffix(doc.CreationDateTime, "Z") {
		t.Errorf("CreationDateTime %q is not UTC (GD-7)", doc.CreationDateTime)
	}
	if doc.Study.OID != "CLARA-"+itoa(projectID) || doc.Study.MetaDataVersion.OID != "CLARA-"+itoa(projectID)+"-MDV" {
		t.Errorf("OIDs = %q / %q (O-1)", doc.Study.OID, doc.Study.MetaDataVersion.OID)
	}
	if doc.Study.GlobalVars.StudyName != "Interchange" {
		t.Errorf("StudyName = %q", doc.Study.GlobalVars.StudyName)
	}

	// The namespace is declared once on the root and inherited, not restated.
	if n := strings.Count(body, `xmlns="`+odmNamespace+`"`); n != 1 {
		t.Errorf("the ODM namespace is declared %d times, want once", n)
	}

	// The audit row is the pipeline's own, with the counts §9 fixes — and it lands
	// from the export goroutine once the row stream settles, so the wait comes before
	// the count rather than after it.
	var details []map[string]any
	for i := 0; i < 50 && len(details) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		details = e.auditDetails(audit.ODMExport, projectID)
	}
	if len(details) != 1 {
		t.Fatalf("odm_export audit rows = %d, want 1: %+v", len(details), details)
	}
	for _, key := range []string{"surface", "sensitivity", "records", "items", "skipped_columns"} {
		if _, ok := details[0][key]; !ok {
			t.Errorf("audit details carry no %q: %+v", key, details[0])
		}
	}
}

func TestExportODMGating(t *testing.T) {
	e := newEnv(t)
	admin := e.mustAdmin("odm-gate@example.org")
	projectID := e.mustProject("Gated")
	outsider := e.mustUser("odm-outsider@example.org")

	if rec := e.do("GET", "/api/v1/projects/"+itoa(projectID)+"/export/odm", nil, outsider); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403 (REQ-ODM-002)", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/projects/999999/export/odm", nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rec.Code)
	}
	// A refused call writes no audit entry (REQ-AUD-004).
	if n := e.auditCount(audit.ODMExport, projectID); n != 0 {
		t.Errorf("refused exports wrote %d audit rows", n)
	}
}
