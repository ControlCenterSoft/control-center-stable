BEGIN;

-- A downgrade must fail closed rather than silently discard network contract
-- objects. Remove or export them explicitly before reverting the schema.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM cc_core_objects
        WHERE object_type IN ('network-zone', 'network-interface')
    ) THEN
        RAISE EXCEPTION 'cannot downgrade while network contract objects exist';
    END IF;
END;
$$;

ALTER TABLE cc_core_objects
    DROP CONSTRAINT cc_core_objects_type;

ALTER TABLE cc_core_objects
    ADD CONSTRAINT cc_core_objects_type CHECK (object_type IN (
        'scope', 'site', 'management-zone', 'role-assignment',
        'desired-state', 'actual-state'
    ));

COMMIT;
