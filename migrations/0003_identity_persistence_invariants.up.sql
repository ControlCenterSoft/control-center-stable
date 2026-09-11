WITH ranked_global_bindings AS (
    SELECT id, row_number() OVER (PARTITION BY user_id, role_name, scope_kind, scope_id ORDER BY created_at, id) AS duplicate_rank
    FROM cc_rbac_user_bindings
)
DELETE FROM cc_rbac_user_bindings AS binding USING ranked_global_bindings AS ranked WHERE binding.id = ranked.id AND ranked.duplicate_rank > 1;
CREATE UNIQUE INDEX cc_rbac_user_bindings_identity_uq ON cc_rbac_user_bindings (user_id, role_name, scope_kind, scope_id) NULLS NOT DISTINCT;
