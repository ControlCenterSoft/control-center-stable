DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'cc_auth_sessions'
          AND column_name = 'last_activity_at'
    ) THEN
        ALTER TABLE cc_auth_sessions
            ADD COLUMN last_activity_at timestamptz;
    END IF;
END
$$;

UPDATE cc_auth_sessions
SET last_activity_at = created_at
WHERE last_activity_at IS NULL;

ALTER TABLE cc_auth_sessions
    ALTER COLUMN last_activity_at SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'cc_auth_sessions'::regclass
          AND conname = 'cc_auth_sessions_activity_window'
    ) THEN
        ALTER TABLE cc_auth_sessions
            ADD CONSTRAINT cc_auth_sessions_activity_window
                CHECK (last_activity_at >= created_at AND last_activity_at <= expires_at);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS cc_auth_sessions_user_activity_idx
    ON cc_auth_sessions (user_id, last_activity_at DESC)
    WHERE revoked_at IS NULL;
