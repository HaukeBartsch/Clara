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
- end_provision (String: delete | anonymize)
- option_radiology (Boolean)
- option_pathology (Boolean)
- option_pathology_type (String, e.g. DICOM)
- option_redcap_only (Boolean)
- option_data_collection_from_home (Boolean)
- agreed_to_end_user_contract (Boolean)
- participant_names (String, participant naming pattern)
- event_names (String, comma-separated initial events)
- creation_time (DateTime)

2. Users
User accounts, created and enabled by admin users.
- id (Primary Key)
- email (String, Unique)
- display_name (String)
- enabled (Boolean)
- auth_source (String: oauth2 | ldap)

3. Roles
A project role is a collection of permissions. Additional roles can be created with a mixture of permissions (import/export/tools).
- id (Primary Key)
- project_id (Foreign Key)
- role_name (String, e.g. data-manager, data-entry, controller)
- permissions (String, set of: view, change, add, export all, export anonymized, project_admin)

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
- An instrument becomes active in the project once it is mapped to an event.

7. Fields (Data Dictionary)
- id (Primary Key)
- project_id (Foreign Key)
- instrument_id (Foreign Key)
- field_name (String, lower-case alphanumeric with underscores, Unique per project)
- field_label (String)
- field_type (String: text, dropdown, radio, matrix, description, header)
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
- period (Integer, days after the first/baseline event of the record)
- safe_region_start (Integer, days before the event, e.g. -2)
- safe_region_end (Integer, days after the event, e.g. +3)
- position (Integer)

9. Instrument-Event Mapping
The checkbox table (instrument x event pairs) that defines the design of a project arm.
- instrument_id (Foreign Key)
- event_id (Foreign Key)
- UNIQUE (instrument_id, event_id)

10. Data (Records)
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
