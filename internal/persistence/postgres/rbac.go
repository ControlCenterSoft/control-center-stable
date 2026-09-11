package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"control-center/internal/identity/rbac"
)

type Authorizer struct{ db *sql.DB }

func NewAuthorizer(db *sql.DB) *Authorizer { return &Authorizer{db: db} }
func (a *Authorizer) Allowed(subjectID string, permission rbac.Permission, target rbac.Scope) bool {
	if a == nil || a.db == nil || subjectID == "" || permission == "" || !target.Valid() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var allowed bool
	err := a.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cc_rbac_user_bindings b JOIN cc_rbac_role_permissions rp ON rp.role_name=b.role_name WHERE b.user_id=$1::uuid AND (rp.permission_name=$2 OR rp.permission_name='*') AND ((b.scope_kind='global' AND b.scope_id IS NULL) OR (b.scope_kind=$3 AND b.scope_id=$4)))`, subjectID, string(permission), string(target.Kind), target.ID).Scan(&allowed)
	return err == nil && allowed
}

func (a *Authorizer) EffectiveGrants(ctx context.Context, subjectID string) ([]rbac.EffectiveGrant, error) {
	if a == nil || a.db == nil || strings.TrimSpace(subjectID) == "" {
		return nil, errors.New("database and subject are required")
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	rows, err := a.db.QueryContext(queryCtx, `SELECT b.role_name,b.scope_kind,COALESCE(b.scope_id,''),rp.permission_name FROM cc_rbac_user_bindings b JOIN cc_rbac_role_permissions rp ON rp.role_name=b.role_name WHERE b.user_id=$1::uuid ORDER BY b.scope_kind,COALESCE(b.scope_id,''),b.role_name,rp.permission_name`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grants := make([]rbac.EffectiveGrant, 0)
	var current *rbac.EffectiveGrant
	for rows.Next() {
		var roleName, scopeKind, scopeID, permission string
		if err := rows.Scan(&roleName, &scopeKind, &scopeID, &permission); err != nil {
			return nil, err
		}
		scope := rbac.Scope{Kind: rbac.ScopeKind(scopeKind), ID: scopeID}
		if !scope.Valid() || strings.TrimSpace(roleName) == "" || strings.TrimSpace(permission) == "" {
			return nil, errors.New("invalid persisted RBAC grant")
		}
		if current == nil || current.RoleName != roleName || current.Scope != scope {
			grants = append(grants, rbac.EffectiveGrant{RoleName: roleName, Scope: scope, Permissions: make([]rbac.Permission, 0, 1)})
			current = &grants[len(grants)-1]
		}
		current.Permissions = append(current.Permissions, rbac.Permission(permission))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return grants, nil
}
