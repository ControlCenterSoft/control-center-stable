package postgres

import (
	"context"
	"database/sql"
	"time"

	"control-center/internal/identity/rbac"
)

type Authorizer struct{ db *sql.DB }

func NewAuthorizer(db *sql.DB) *Authorizer { return &Authorizer{db: db} }

// Allowed fails closed on malformed input, database errors, and timeouts.
func (a *Authorizer) Allowed(subjectID string, permission rbac.Permission, target rbac.Scope) bool {
	if a == nil || a.db == nil || subjectID == "" || permission == "" || !target.Valid() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var allowed bool
	err := a.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM cc_rbac_user_bindings b
  JOIN cc_rbac_role_permissions rp ON rp.role_name=b.role_name
  WHERE b.user_id=$1::uuid
    AND (rp.permission_name=$2 OR rp.permission_name='*')
    AND (
      (b.scope_kind='global' AND b.scope_id IS NULL)
      OR (b.scope_kind=$3 AND b.scope_id=$4)
    )
)`, subjectID, string(permission), string(target.Kind), target.ID).Scan(&allowed)
	return err == nil && allowed
}
