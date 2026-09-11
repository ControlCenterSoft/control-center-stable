BEGIN;

DROP TABLE IF EXISTS cc_core_object_mutations;
DROP TABLE IF EXISTS cc_core_objects;

DELETE FROM cc_rbac_role_permissions
WHERE permission_name IN ('core.objects.read', 'core.objects.write');

DELETE FROM cc_rbac_permissions
WHERE name IN ('core.objects.read', 'core.objects.write');

COMMIT;
