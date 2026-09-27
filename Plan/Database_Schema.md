Database Schema - Clinical Study Management System

This document outlines the core database tables required for the system. The schema is created by SQL statements that create or alter an existing database, and is designed to be compatible with both SQLite (development) and MariaDB (production) using only SQL features common to both.

Core Tables

1. Projects
Stores project-level metadata (see the project creation form in Endpoints.md).
- id (Primary Key)
- project_name (String, Unique)
- organization (String: OTHER, VEST, HBE, SUS, FOR, FON, UIB, UIS, HVL, NAT EU)
- pi_name (String)
- pi_email (String)
- dm_name (String, Data Manager)
- dm_email (String)
- rek_number (String)
- rek_start_date (Date)
- rek_end_date (Date)
- start_date (Date)
- end_date (Date)
- participant_names (String, participant naming pattern)
- mode (String: development | production | analysis, default development - project modes)
- creation_time (DateTime)

Removed from this table by GD-17 (2026-09-22) and no longer project metadata: `end_provision`, the `option_*` flags (`radiology`, `pathology`, `pathology_type`, `redcap_only`, `data_collection_from_home`), `agreed_to_end_user_contract`, and `event_names` (initial events). The owner MAY hold them as data in an ordinary instrument (e.g. `DataTransferProjects`); initial events are added through the setup endpoints after creation (REQ-DB-032, DEV-DB-5).

2. Users
User accounts, created and enabled by admin users.
- id (Primary Key)
- email (String, Unique)
- display_name (String)
- enabled (Boolean)
- auth_source (String: oauth2 | ldap | local - GD-18 adds the table-based local password path)
- password_hash (String, nullable - bcrypt hash, set only for `local` accounts; GD-18)
- valid_until (DateTime, nullable - account validity end from `valid_days`, `0`/null = indefinite; GD-19)
- last_login_at (DateTime, nullable - drives the inactivity auto-disable; GD-19)
- is_admin (Boolean)
- ui_language (String, default en)
- created_at (DateTime)

3. Roles
A project role is a collection of permissions, defined per project (any name, any combination — not a fixed catalogue).
- id (Primary Key)
- project_id (Foreign Key)
- role_name (String, e.g. data-manager, data-entry, controller)
- project_admin (Boolean - may manage the project's structure and membership)

3a. Role-Arm Permissions
Permissions are held **per arm** (GD-2), one row per (role, arm) — not a flat permission string:
- role_id (Foreign Key)
- arm_num (Integer)
- data_access_level (String: no_access | read_only | view_edit | delete | edit_survey_responses)
- export_level (String: export_none | export_de_identified | export_no_identifiers | export_full)

An arm with no row means no access. The former flat set (`view`, `change`, `add`, `export all`, `export anonymized`) is superseded by these two ordered levels (REQ-AUTH-017/018, DEV-AUTH-5).

4. User-Project Assignments
Maps users to projects, roles, and API tokens.
- id (Primary Key)
- user_id (Foreign Key)
- project_id (Foreign Key)
- role_id (Foreign Key, nullable - a role-less member has full permissions)
- token (UUID, identifies the user for this project)
- UNIQUE (user_id, project_id)

5. Arms
- id (Primary Key)
- project_id (Foreign Key)
- arm_num (Integer, 1-based)
- name (String)
- A single arm is assumed to start with.

6. Instruments
- id (Primary Key)
- project_id (Foreign Key)
- name (String, Unique per project)
- position (Integer, order of the instrument in the project; the record field of the first instrument becomes the record_id field for the project)
- is_survey (Boolean - only survey-marked instruments may be filled out through a public survey link; GD-9)
- An instrument becomes active in the project once it is mapped to an event.

7. Fields (Data Dictionary)
- id (Primary Key)
- project_id (Foreign Key)
- instrument_id (Foreign Key)
- field_name (String, lower-case alphanumeric with underscores, Unique per project)
- field_label (String)
- field_type (String: text, dropdown, radio, matrix, description, header, calculated)
- calculation_expression (Text, when type is `calculated`; GD-11)
- section_header (String)
- choices (Text, numeric code + label pairs for dropdown/radio/matrix)
- field_note (Text)
- validation_type (String: built-in integer, floating point, date, datetime; or a named regular expression from the validation_types table: email, MRN, international phone, national phone, ...)
- validation_min (String)
- validation_max (String)
- required (Boolean)
- branching_logic (Text)
- matrix_group (String)
- personal_information (Boolean, used for anonymized export)
- direct_identifier (Boolean, user-set on any field; preset for email/MRN/phone types; removed at de-identified export)
- position (Integer, order of the field within the instrument)

7a. Validation Types (extensible registry, system-wide)
- name (Primary Key: e.g. email, MRN, international phone, national phone)
- regex (Text, Go RE2 pattern matched against the whole value)
- builtin (Boolean, seeded entries cannot be removed)

8. Events
- id (Primary Key)
- project_id (Foreign Key)
- arm_id (Foreign Key)
- event_name (String, label, e.g. baseline)
- unique_event_name (String, label + _arm_N)
- period (Integer, nullable: days after the first/baseline event of the record; NULL = the event has no timepoint. Canonical per-arm order: events with a period sort by it ascending, ties by position; NULL-period events follow, ordered by position - GD-15)
- safe_region_start (Integer, days before the event, e.g. -2)
- safe_region_end (Integer, days after the event, e.g. +3)
- position (Integer)

9. Instrument-Event Mapping
The checkbox table (instrument x event pairs) that defines the design of a project arm.
- instrument_id (Foreign Key)
- event_id (Foreign Key)
- UNIQUE (instrument_id, event_id)

10. Project Staging (production mode only)
At most one open staging set per project: a snapshot of the staged design, applied to the live structure tables on commit (all in one transaction) and removed on commit or discard.
- project_id (Primary Key, Foreign Key - one open set per project)
- design (Text, JSON snapshot of the staged design: instruments with fields, arms with events, instrument-event mapping)
- opened_by (Foreign Key, nullable)
- opened_at (DateTime)

11. Data (Records)
Entity-attribute-value layout, exactly as mandated by Endpoints.md: adding a new field or a new project never changes this table layout.
- project_id (Foreign Key)
- record_id (String)
- unique_event_name (String, includes arm information)
- repeating_instrument (String, instrument name)
- repeating_instance_number (Integer, starts with 1)
- field_name (String)
- value (Long Text / Binary)
- UNIQUE (project_id, record_id, unique_event_name, repeating_instrument, repeating_instance_number, field_name) - it is not possible to add a second value for the same combination
- INDEX (project_id) - fast lookup of all values for one project
- INDEX (record_id) - fast lookup of all values for one record_id

12. Instrument Completion
The user's "finished" assignment at the end of a data-collection instrument — the third state of the record-status dashboard (grey / amber / green). Sparse: a row exists only where the user marked it finished, so absence means not finished and the no-data / some-data split stays derived from the values above (REQ-DB-036, DEV-DB-10).
- project_id (Foreign Key)
- record_id (String)
- event_id (Foreign Key)
- instrument_id (Foreign Key)
- completed_by (Foreign Key, nullable)
- completed_at (DateTime)
- PRIMARY KEY (project_id, record_id, event_id, instrument_id) - also the dashboard read path
- Survey instruments hold no completion state (GD-9).
