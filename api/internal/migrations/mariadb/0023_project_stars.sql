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
    user_id    INTEGER NOT NULL,              -- same type as users.id (FK)
    project_id INTEGER NOT NULL,              -- same type as projects.id (FK)
    created_at DATETIME NOT NULL,             -- UTC
    PRIMARY KEY (user_id, project_id),
    KEY idx_project_stars_project (project_id),
    CONSTRAINT fk_project_stars_user    FOREIGN KEY (user_id)    REFERENCES users(id)    ON DELETE CASCADE,
    CONSTRAINT fk_project_stars_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
) ENGINE=InnoDB;
