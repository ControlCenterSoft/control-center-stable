BEGIN;

-- Remove only grants that 0008 itself inserted. A same-named grant that was
-- already present on 0.3, including a grant to a custom role, is not tracked
-- and therefore survives the downgrade.
DELETE FROM cc_rbac_role_permissions AS grant_row
USING cc_migration_0008_rbac_seed AS seeded
WHERE seeded.item_kind = 'role-grant'
  AND grant_row.role_name = seeded.role_name
  AND grant_row.permission_name = seeded.permission_name;

-- A permission introduced by 0008 is retained if a custom grant now refers
-- to it. This makes rollback conservative instead of cascading user RBAC.
DELETE FROM cc_rbac_permissions AS permission
USING cc_migration_0008_rbac_seed AS seeded
WHERE seeded.item_kind = 'permission'
  AND permission.name = seeded.permission_name
  AND NOT EXISTS (
      SELECT 1
      FROM cc_rbac_role_permissions AS remaining_grant
      WHERE remaining_grant.permission_name = permission.name
  );

DROP TABLE cc_migration_0008_rbac_seed;

COMMIT;
