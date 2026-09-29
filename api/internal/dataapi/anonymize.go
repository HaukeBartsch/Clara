package dataapi

// In-place anonymization (Data_Export_Anonymization_Design.md §7.4): the
// export_de_identified pipeline applied to the stored values themselves, so
// every later read and export — including export_full — returns the
// anonymized form. Serves the end-provision action (§7.2); the administration
// handler wraps it in its transaction with the project_ended audit row.

import (
	"context"
	"database/sql"

	"csms/api/internal/db"
)

// AnonymizeProjectTx rewrites every stored value of the project per §7.4,
// inside the caller's transaction: direct identifiers cleared, personal
// values replaced by the §5.1 hash, unapproved free text cleared, and
// date-bearing values shifted by the record's persisted offset (§5.2 —
// computed and persisted first). Values that already carry the target form
// are left alone. Returns (recordsAffected, valuesAffected).
func (h *Handler) AnonymizeProjectTx(ctx context.Context, tx *sql.Tx, projectID int64) (int64, int64, error) {
	d, err := h.loadDict(ctx, projectID)
	if err != nil {
		return 0, 0, err
	}
	type stored struct {
		record, event, repInstr, field, value string
		repNum                                int
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT record_id, unique_event_name, repeating_instrument,
		        repeating_instance_number, field_name, value
		 FROM data WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, 0, err
	}
	var all []stored
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.record, &s.event, &s.repInstr, &s.repNum, &s.field, &s.value); err != nil {
			rows.Close()
			return 0, 0, err
		}
		all = append(all, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()

	offsets := map[string]int{} // per-record §5.2 offset, computed on demand
	recordsTouched := map[string]bool{}
	var valuesChanged int64
	for _, s := range all {
		rf, known := d.byName[s.field]
		if !known {
			continue // field no longer in the dictionary — nothing classifies it
		}
		nv := s.value
		switch categorize(rf, d.identifier) {
		case catDirectID:
			nv = ""
		case catPersonal:
			if nv != "" {
				nv = fieldHash(h.Cfg.AnonSalt, projectID, s.field, nv)
			}
		case catFreeText:
			if !rf.ExportApproved {
				nv = ""
			}
		default: // catStructured — kept; date shift applies to its own values
		}
		if nv != "" && isDateBearing(rf.Field) {
			off, ok := offsets[s.record]
			if !ok {
				var err error
				off, err = db.EnsureAnonOffsetTx(ctx, tx, projectID, s.record,
					h.Cfg.AnonSalt, h.Cfg.AnonDateShiftMin, h.Cfg.AnonDateShiftMax)
				if err != nil {
					return 0, 0, err
				}
				offsets[s.record] = off
			}
			nv = shiftDatePart(nv, off)
		}
		if nv == s.value {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE data SET value = ? WHERE project_id = ? AND record_id = ?
			 AND unique_event_name = ? AND repeating_instrument = ?
			 AND repeating_instance_number = ? AND field_name = ?`,
			nv, projectID, s.record, s.event, s.repInstr, s.repNum, s.field); err != nil {
			return 0, 0, err
		}
		valuesChanged++
		recordsTouched[s.record] = true
	}
	return int64(len(recordsTouched)), valuesChanged, nil
}
