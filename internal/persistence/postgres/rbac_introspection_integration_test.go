package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"control-center/internal/identity/rbac"
	"control-center/internal/identity/security"
)

func TestPostgresEffectiveGrantsReturnsOnlyPersistedSubjectBindings(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; PostgreSQL integration test skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var rbacPresent bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cc_rbac_user_bindings') IS NOT NULL AND to_regclass('cc_rbac_role_permissions') IS NOT NULL`).Scan(&rbacPresent); err != nil || !rbacPresent {
		t.Fatalf("database must include RBAC migrations: present=%v err=%v", rbacPresent, err)
	}

	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash("rbac-introspection-integration-password")
	if err != nil {
		t.Fatal(err)
	}
	username := fmt.Sprintf("rbac-introspection-%d", time.Now().UnixNano())
	userID, created, err := BootstrapAdmin(ctx, db, username, passwordHash, time.Now().UTC())
	if err != nil || !created {
		t.Fatalf("bootstrap created=%v err=%v", created, err)
	}
	defer db.ExecContext(context.Background(), `DELETE FROM cc_local_users WHERE id=$1::uuid`, userID)

	grants, err := NewAuthorizer(db).EffectiveGrants(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("grants=%#v, want one administrator grant", grants)
	}
	grant := grants[0]
	if grant.RoleName != "administrator" || grant.Scope != rbac.GlobalScope() || len(grant.Permissions) != 1 || grant.Permissions[0] != rbac.PermissionAll {
		t.Fatalf("unexpected administrator grant: %#v", grant)
	}
}
