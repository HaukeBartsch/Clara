-- Per-user UI theme override (GD-26, REQ-DB-008, DEV-DB-13). Nullable like a
-- preference should be: NULL means "follow the installation default"
-- (UI_THEME, REQ-CFG-031), so no existing row needs a backfill. The value is
-- an installed theme identifier (bootstrap | darkly | yeti, REQ-TECH-027)
-- enforced in code, not by CHECK — the selectable set is the set of vendored
-- theme files and may grow without a schema change (mirrors projects.mode).
ALTER TABLE users ADD COLUMN ui_theme TEXT;
