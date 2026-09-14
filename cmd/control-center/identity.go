package main

import (
	"context"
	"database/sql"
	"time"

	"control-center/internal/identity/auth"
	identityapi "control-center/internal/identity/httpapi"
	"control-center/internal/identity/security"
	"control-center/internal/persistence/postgres"
)

func newIdentityHandler(environment string, db *sql.DB, sessionTTL, sessionIdleTimeout time.Duration) (*identityapi.Server, error) {
	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.HashBootstrapAdminPassword()
	if err != nil {
		return nil, err
	}
	auditLog, err := postgres.NewAuditLog(db)
	if err != nil {
		return nil, err
	}
	if err := auditLog.VerifyChain(context.Background()); err != nil {
		return nil, err
	}
	if _, _, err := postgres.BootstrapAdmin(context.Background(), db, "admin", passwordHash, time.Now().UTC()); err != nil {
		return nil, err
	}
	store, err := postgres.NewIdentityStore(db)
	if err != nil {
		return nil, err
	}
	authService, err := auth.NewService(store, store, auditLog, hasher, sessionTTL, auth.WithSessionIdleTimeout(sessionIdleTimeout))
	if err != nil {
		return nil, err
	}
	authorizer := postgres.NewAuthorizer(db)

	return identityapi.NewServerWithAuditExport(authService, authorizer, auditLog, identityapi.Config{
		InsecureCookiesForDevelopment: environment == "development" || environment == "test",
	})
}
