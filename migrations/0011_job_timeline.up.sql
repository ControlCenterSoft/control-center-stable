CREATE TABLE IF NOT EXISTS cc_job_timeline (
    job_id       text        NOT NULL REFERENCES cc_jobs(id) ON DELETE CASCADE,
    job_version  bigint      NOT NULL CHECK (job_version > 0),
    event        text        NOT NULL CHECK (event IN (
                    'observed', 'created', 'attempt_started', 'retry_scheduled',
                    'cancel_requested', 'cancelled', 'succeeded', 'failed'
                 )),
    status       text        NOT NULL CHECK (status IN (
                    'queued', 'running', 'retry_wait', 'cancel_requested',
                    'cancelled', 'succeeded', 'failed'
                 )),
    attempt      integer     NOT NULL CHECK (attempt >= 0),
    occurred_at  timestamptz NOT NULL,
    PRIMARY KEY (job_id, job_version)
);

CREATE INDEX IF NOT EXISTS cc_job_timeline_order_idx
    ON cc_job_timeline (job_id, occurred_at, job_version);

-- Existing jobs predate the lifecycle journal. Preserve their current durable
-- state as bounded baseline evidence instead of inventing transitions that were
-- never recorded.
INSERT INTO cc_job_timeline (job_id, job_version, event, status, attempt, occurred_at)
SELECT id, version, 'observed', status, attempt, updated_at
FROM cc_jobs
ON CONFLICT (job_id, job_version) DO NOTHING;
