User Interface Plan

This document outlines the key views and layout for the clinical study management system, built using JavaScript, HTML, and Bootstrap, and served by the PHP web application. The interface exclusively uses the API to create projects, edit the setup of a project, and to store values into fields for projects.

Key Views

1. Dashboard
- Purpose: Start page after login; overview of accessible projects and recent activity.
- Components: List of projects the user has access to (administrator group or project membership), quick stats (record counts), and notifications.

2. Admin Interface
- Purpose: Administration for authenticated and authorized (administrator) users.
- Components:
    - User accounts: Create (enable) and manage normal user accounts.
    - Projects: Create new projects (name, organization, PI, data manager, REK number, REK start/end dates, end provision, options) and edit project metadata.
    - Assignment: Assign users to projects given a role (data-manager, data-entry, controller, or a custom role). Role-less members have full permissions.
    - Roles: Create additional roles with a mixture of permissions.

3. Project Workspace
- Purpose: Main hub for a specific study, shown when the user selects a project.
- Components:
    - Summary: Number of records, instruments, and fields in the project.
    - Actions (presented only if the user's role/permission allows them):
        - Setup (permission "project_admin"): Add/remove arms, instruments, events, and mappings between them.
        - Design: Create a new instrument, edit fields in an existing instrument.
        - Record status dashboard: Show the table of records and instruments by event.
        - Export: Export project data based on the user's export permissions (full or anonymized).

4. Instrument Designer
- Purpose: Edit an instrument's data dictionary.
- Components: Select an instrument; edit, add, and change its fields; reorder the fields; field properties (name with a warning after 26 characters, label, type, choices, validation, required, personal-information flag).
- The instrument list of a project can be reordered; the record field of the first instrument automatically becomes the record_id field for the project.
- A mapping page shows the instrument-by-event mapping (table with checkboxes) for each arm. A single arm is assumed to start with.

5. Data Entry Form
- Purpose: Collecting clinical data.
- Components:
    - Dynamic rendering of fields (Text, Dropdowns, Radio buttons, Matrix).
    - Real-time validation feedback (client-side JavaScript + HTML5; authoritative validation in the API).

6. Record Status Dashboard
- Purpose: Overview of data completion for a project.
- Components: Lists all record_ids in a project with their instruments sorted by event and ordered based on the order of instruments in each arm. Each instrument is rendered with a small graphic (filled vs. gray circle) indicating whether any of its fields has a value.

UI Framework
- Framework: Bootstrap
- Description: Used for responsive layout and pre-built components (forms, buttons, modals) on plain JavaScript and HTML.
