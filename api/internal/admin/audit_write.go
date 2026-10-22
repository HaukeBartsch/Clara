package admin

import (
	"context"
	"database/sql"

	"csms/api/internal/audit"
)

// auditTx runs one mutation and the audit entry describing it inside a single
// transaction, so neither can land without the other (REQ-AUD-003, DEV-AUD-1).
//
// This is the shape every write handler should use. Calling a plain Store mutator
// and then Audit.Insert leaves a window in which the change commits and the entry
// does not — a crash or a failed insert between the two records nothing, which is
// the quiet direction of failure for a system whose compliance story rests on the
// trail. The data path already worked this way (InsertTx alongside the record
// write); these handlers did not.
//
// mutate performs the change with the caller's transaction; entry is written in
// that same transaction, before Commit. A caller that needs a generated id after
// the commit declares it before the call and assigns it inside mutate.
func (h *Handler) auditTx(ctx context.Context, mutate func(tx *sql.Tx) error, entry audit.Entry) error {
	tx, err := h.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := mutate(tx); err != nil {
		return err
	}
	if err := h.Audit.InsertTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit()
}

// auditTxStructure is auditTx for a change to the project design (arms, events,
// instruments, fields, the mapping): it bumps the structure cache only after the
// commit lands. A bump inside the transaction would let a concurrent request
// repopulate the cache from the pre-commit state between the bump and the commit,
// and keep serving that answer after the change became visible (db.AddArmTx).
func (h *Handler) auditTxStructure(ctx context.Context, mutate func(tx *sql.Tx) error, entry audit.Entry) error {
	if err := h.auditTx(ctx, mutate, entry); err != nil {
		return err
	}
	h.Store.BumpStructureCache()
	return nil
}
