DROP INDEX IF EXISTS cc_auth_sessions_user_activity_idx;

ALTER TABLE cc_auth_sessions
    DROP CONSTRAINT IF EXISTS cc_auth_sessions_activity_window,
    DROP COLUMN IF EXISTS last_activity_at;
