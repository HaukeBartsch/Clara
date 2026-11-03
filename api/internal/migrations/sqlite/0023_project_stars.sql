-- Starred projects (REQ-DB-056, REQ-UI-087): the projects a user marked with a star
-- on the project overview, so that the overview can show those alone. One row is
-- one star; unstarring deletes it. A star orders and filters presentation only: no
-- authorization path reads this table, and a star on a project the user can no
-- longer see is never listed (REQ-API-049 filters by visibility first), so it
-- returns unchanged if the membership does.
--
-- Both foreign keys cascade: a deleted project (REQ-EXP-044) or account takes its
-- stars with it.
CREATE TABLE IF NOT EXISTS project_stars (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,                 -- UTC "YYYY-MM-DD HH:MM:SS"
    PRIMARY KEY (user_id, project_id)
);

-- The cascade from projects looks rows up by project_id.
CREATE INDEX IF NOT EXISTS idx_project_stars_project ON project_stars (project_id);
