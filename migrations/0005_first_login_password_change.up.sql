ALTER TABLE cc_local_users
    ADD COLUMN password_change_required boolean NOT NULL DEFAULT false;

ALTER TABLE cc_auth_sessions
    ADD COLUMN credential_version timestamptz;

UPDATE cc_auth_sessions AS session
SET credential_version = local_user.password_changed_at
FROM cc_local_users AS local_user
WHERE local_user.id = session.user_id;

ALTER TABLE cc_auth_sessions
    ALTER COLUMN credential_version SET NOT NULL;

COMMENT ON COLUMN cc_local_users.password_change_required IS
    'Blocks ordinary authenticated operations until the user selects a policy-compliant password';

COMMENT ON COLUMN cc_auth_sessions.credential_version IS
    'Password version authenticated when this session was issued';
