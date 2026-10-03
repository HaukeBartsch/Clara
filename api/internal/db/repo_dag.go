package db

import (
	"context"
	"database/sql"
)

// Data access groups (REQ-DB-028, GD-10) — the transactional forms the §4.18
// endpoints need, so a group change and its audit entry commit together
// (REQ-AUD-003). The single-statement readers live in repo_design.go alongside
// the rest of the dag_groups / dag_memberships surface; what is collected here
// is the write side, where "one active group per assignment" and "no group
// disappears under still-assigned records" are storage-level invariants worth
// expressing once rather than in each handler.

// CreateDAGGroupTx inserts a group inside tx (REQ-DB-028). The caller checks
// name uniqueness — the 409 of REQ-API-087 is an API decision, not a
// constraint violation to be recovered from here.
func (s *Store) CreateDAGGroupTx(ctx context.Context, tx *sql.Tx, dg *DagGroup) error {
	dg.CreatedAt = nowUTC()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO dag_groups (project_id, name, created_at) VALUES (?, ?, ?)`,
		dg.ProjectID, dg.Name, dg.CreatedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	dg.ID = id
	return nil
}

// DeleteDAGGroupTx removes a group inside tx; its member rows go with it
// (dag_memberships cascades on the group). The caller has established that no
// record is still assigned — REQ-AUTH-043, ASM-AUTH-4.
func (s *Store) DeleteDAGGroupTx(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM dag_groups WHERE id = ?`, id)
	return err
}

// SetDAGMembershipsTx replaces one member's whole group set (REQ-AUTH-044):
// the memberships become exactly groupIDs, with activeGroupID the active one
// (0 leaves the member without an active group — the state REQ-AUTH-046 calls
// "no group selected"). An empty groupIDs list clears the member.
func (s *Store) SetDAGMembershipsTx(ctx context.Context, tx *sql.Tx, assignmentID int64,
	groupIDs []int64, activeGroupID int64) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM dag_memberships WHERE assignment_id = ?`, assignmentID); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dag_memberships (assignment_id, group_id, is_active) VALUES (?, ?, ?)`,
			assignmentID, gid, boolToInt(gid == activeGroupID)); err != nil {
			return err
		}
	}
	return nil
}

// SetDAGActiveTx makes one membership the member's active group and deactivates
// its siblings in the same transaction (REQ-AUTH-046): at most one row per
// assignment ever reads is_active = 1, so no half-switch is observable.
func (s *Store) SetDAGActiveTx(ctx context.Context, tx *sql.Tx, assignmentID, groupID int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE dag_memberships SET is_active = 0 WHERE assignment_id = ?`, assignmentID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE dag_memberships SET is_active = 1 WHERE assignment_id = ? AND group_id = ?`,
		assignmentID, groupID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 { // not one of the member's groups — the caller validated this
		return sql.ErrNoRows
	}
	return nil
}

// SetRecordDAGTx assigns a record to a group inside tx, or unassigns it when
// dagGroupID is null (REQ-AUTH-048). Only an existing record entity can be
// assigned; the caller checks RecordExists first.
func (s *Store) SetRecordDAGTx(ctx context.Context, tx *sql.Tx, projectID int64,
	recordID string, dagGroupID sql.NullInt64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE record_entities SET dag_group_id = ? WHERE project_id = ? AND record_id = ?`,
		nullInt64(dagGroupID), projectID, recordID)
	return err
}

// CountRecordsByDAGGroup counts the records currently assigned to a group —
// the guard that keeps a group from being deleted out from under them
// (ASM-AUTH-4; REQ-API-088 answers 409 while this is non-zero).
func (s *Store) CountRecordsByDAGGroup(ctx context.Context, projectID, groupID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM record_entities WHERE project_id = ? AND dag_group_id = ?`,
		projectID, groupID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ListDAGMembershipsByProject returns every data-access-group membership held by
// the project's members (REQ-API-141): one read for a whole members listing, so
// showing who holds which group costs one query instead of one per member
// (Plan/Web_Implementation.md §7 rule 13). Group names are not joined — the
// caller already has the project's groups from ListDAGGroups and pairs them by
// id — and rows come back ordered by assignment then group, which is the order a
// member row lists its groups in.
func (s *Store) ListDAGMembershipsByProject(ctx context.Context, projectID int64) ([]DagMembership, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT m.id, m.assignment_id, m.group_id, m.is_active
			FROM dag_memberships m
			JOIN user_projects a ON a.id = m.assignment_id
			WHERE a.project_id = ?
			ORDER BY m.assignment_id, m.group_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DagMembership
	for rows.Next() {
		dm, err := scanDagMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dm)
	}
	return out, rows.Err()
}
