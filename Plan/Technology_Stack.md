Technology Stack

Frontend
- Languages: JavaScript, HTML
- Framework: Bootstrap (responsive layout, forms, buttons, modals)
- Description: Plain implementation for the webpage interface; all data access goes through the API.

Backend
- Language: PHP
- Description: Serves the web application (page rendering, sessions, OAuth2/LDAP login flow). All data access to the backend is delegated to the API; the PHP layer does not write to the database directly.

API
- Language: Go (Golang)
- Tools: Swagger, OpenAPI
- Description: API implementation for external integrations (REDCap-compatible protocol) and for the web application.

Database
- Development Default: SQLite
- Production/Alternative: MariaDB
- Description: The system will support switching between SQLite and MariaDB depending on the environment. SQL statements target features common to both databases, with documented exceptions for MariaDB-only features (e.g. audit table partitioning).
