Project Charter - Clinical Study Management System

Project Overview
This project aims to develop a new research electronic data capture system for clinical studies. The system will provide a web-based platform to collect project data securely and efficiently, and integrates with the research information system through a REDCap-compatible API.

Objectives
- Create a secure, token-based access system mapped to user accounts (OAuth2 with LDAP fallback).
- Implement granular, project-scoped permissions for different user roles.
- Support flexible project structures including study arms, events, and instruments.
- Ensure data integrity through robust field validations.
- Expose a REDCap-compatible API (Go, OpenAPI/Swagger) so existing callers such as Fiona keep working unchanged.
- Provide a web user interface (admin interface and data entry) that accesses the backend exclusively through the API.
- Log all access to backend data in two audit tables: one for change/create/delete events, and one exclusively for record views (records pulled through the API).

Scope
The initial phase will focus on the core data model as defined in Endpoints.md, including user management, project structure, the data dictionary, the REDCap-compatible API, the web user interfaces, and audit logging. The API is implemented in Go, the web application in PHP, and the database runs on MariaDB in production and SQLite in development.
