BEGIN;

DO $migration$
DECLARE
    current_type text;
BEGIN
    SELECT data_type
      INTO current_type
      FROM information_schema.columns
     WHERE table_schema = 'public'
       AND table_name = 'cc_config_revisions'
       AND column_name = 'content';

    IF current_type IS NULL THEN
        RAISE EXCEPTION 'cc_config_revisions.content is missing';
    ELSIF current_type = 'jsonb' THEN
        ALTER TABLE cc_config_revisions
            ALTER COLUMN content TYPE json
            USING content::json;
    ELSIF current_type <> 'json' THEN
        RAISE EXCEPTION 'unsupported cc_config_revisions.content type: %', current_type;
    END IF;
END;
$migration$;

COMMIT;
