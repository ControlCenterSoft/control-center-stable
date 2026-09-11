BEGIN;

-- Extend the existing generic distributed object store without changing or
-- rewriting any supported 0.3 tables or existing 0.4 objects.
ALTER TABLE cc_core_objects
    DROP CONSTRAINT cc_core_objects_type;

ALTER TABLE cc_core_objects
    ADD CONSTRAINT cc_core_objects_type CHECK (object_type IN (
        'scope', 'site', 'management-zone', 'network-zone', 'network-interface',
        'role-assignment', 'desired-state', 'actual-state'
    ));

COMMIT;
