ALTER TABLE cc_local_users
    DROP COLUMN password_change_required;

ALTER TABLE cc_auth_sessions
    DROP COLUMN credential_version;
