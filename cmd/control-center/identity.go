package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"control-center/internal/identity/auth"
	identityapi "control-center/internal/identity/httpapi"
	"control-center/internal/identity/security"
	"control-center/internal/persistence/postgres"
)

func newIdentityHandler(environment string, db *sql.DB, username, password string) (*identityapi.Server, error) {
	if username == "" || password == "" {
		return nil, errors.New("CC_BOOTSTRAP_ADMIN_USERNAME and CC_BOOTSTRAP_ADMIN_PASSWORD are required")
	}

	hasher := security.NewPasswordHasher()
	passwordHash, err := hasher.Hash(password)
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
	if _, err := postgres.BootstrapAdmin(context.Background(), db, username, passwordHash, time.Now().UTC()); err != nil {
		return nil, err
	}
	store, err := postgres.NewIdentityStore(db)
	if err != nil {
		return nil, err
	}
	authService, err := auth.NewService(store, store, auditLog, hasher, 0)
	if err != nil {
		return nil, err
	}
	authorizer := postgres.NewAuthorizer(db)

	return identityapi.NewServer(authService, authorizer, auditLog, identityapi.Config{
		InsecureCookiesForDevelopment: environment == "development" || environment == "test",
	})
}
