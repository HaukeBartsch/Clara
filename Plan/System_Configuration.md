System Configuration Plan

This document outlines the configuration management for the clinical study management system, supporting different environments.

Configuration Management
- Method: Environment variables stored in a .env file (not tracked in version control).
- Example Configuration:

# Environment
APP_ENV=development

# Database
DB_CONNECTION=sqlite
DB_DATABASE=/path/to/database.sqlite
# DB_CONNECTION=mariadb
# DB_HOST=localhost
# DB_PORT=3306
# DB_DATABASE=clinical_db
# DB_USERNAME=root
# DB_PASSWORD=secret

# Authentication
OAUTH_PROVIDER_URL=https://auth.example.com
LDAP_SERVER_1=ldap://ldap1.example.com
LDAP_SERVER_2=ldap://ldap2.example.com
LDAP_SERVER_3=ldap://ldap3.example.com

Key Settings
- APP_ENV: Toggles between development (SQLite default) and production (MariaDB).
- DB_*: Database connection details.
- OAUTH_ / LDAP_*:* Authentication server details.
