BEGIN;

DROP INDEX IF EXISTS cc_incident_affected_resources_lookup_idx;
DROP TABLE IF EXISTS cc_incident_affected_resources;
DROP INDEX IF EXISTS cc_incident_read_models_severity_order_idx;
DROP INDEX IF EXISTS cc_incident_read_models_status_order_idx;
DROP INDEX IF EXISTS cc_incident_read_models_scope_order_idx;
DROP TABLE IF EXISTS cc_incident_read_models;

COMMIT;
