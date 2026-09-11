package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"control-center/internal/buildinfo"
	commonapi "control-center/internal/httpapi"
	"control-center/internal/identity/audit"
	"control-center/internal/identity/auth"
	"control-center/internal/identity/rbac"
)

const DefaultSessionCookie = "cc_session"

type Server struct {
	auth          *auth.Service
	authorizer    rbac.Checker
	audit         audit.Logger
	cookieName    string
	secureCookies bool
	mux           *http.ServeMux
}

type Config struct {
	CookieName                    string
	InsecureCookiesForDevelopment bool
}

func NewServer(authService *auth.Service, authorizer rbac.Checker, log audit.Logger, config Config) (*Server, error) {
	if authService == nil || authorizer == nil || log == nil {
		return nil, errors.New("auth service, authorizer and audit logger are required")
	}
	cookieName := config.CookieName
	if cookieName == "" {
		cookieName = DefaultSessionCookie
	}
	s := &Server{
		auth: authService, authorizer: authorizer, audit: log,
		cookieName: cookieName, secureCookies: !config.InsecureCookiesForDevelopment,
		mux: http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/v1/auth/login", s.login)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	s.mux.Handle("GET /api/v1/auth/session", s.Authenticate(http.HandlerFunc(s.session)))
	s.mux.Handle("GET /api/v1/auth/session-policy", s.Authenticate(s.RequirePasswordCurrent(http.HandlerFunc(s.sessionPolicy))))
	s.mux.Handle("POST /api/v1/auth/password", s.Authenticate(http.HandlerFunc(s.changePassword)))
	s.mux.Handle("GET /api/v1/auth/sessions", s.Authenticate(s.RequirePasswordCurrent(http.HandlerFunc(s.listSessions))))
	s.mux.Handle("DELETE /api/v1/auth/sessions/{sessionID}", s.Authenticate(s.RequirePasswordCurrent(http.HandlerFunc(s.revokeSession))))
	s.mux.Handle("POST /api/v1/auth/sessions/revoke-all", s.Authenticate(s.RequirePasswordCurrent(http.HandlerFunc(s.revokeAllSessions))))
	s.mux.Handle("GET /api/v1/identity/self", s.Authenticate(s.RequirePasswordCurrent(http.HandlerFunc(s.identitySelf))))
	s.mux.Handle("GET /api/v1/system/overview", s.Authenticate(s.Require(rbac.PermissionOverviewRead, rbac.GlobalScope())(http.HandlerFunc(s.overviewAPI))))

	s.mux.HandleFunc("GET /login", s.webLogin)
	s.mux.HandleFunc("POST /web/login", s.webLoginSubmit)
	s.mux.Handle("GET /password/change", s.Authenticate(http.HandlerFunc(s.webPasswordChange)))
	s.mux.Handle("POST /web/password/change", s.Authenticate(http.HandlerFunc(s.webPasswordChangeSubmit)))
	s.mux.Handle("GET /overview", s.Authenticate(s.Require(rbac.PermissionOverviewRead, rbac.GlobalScope())(http.HandlerFunc(s.webOverview))))
	s.mux.HandleFunc("POST /web/logout", s.webLogout)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	s.mux.ServeHTTP(w, r)
}

type contextKey int

const principalKey contextKey = iota

type Principal struct {
	Token                  string
	Session                auth.SessionView
	Identity               auth.Identity
	PasswordChangeRequired bool
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey).(Principal)
	return principal, ok
}

func (s *Server) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.tokenFromRequest(r)
		authenticated, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		principal := Principal{Token: token, Session: authenticated.Session, Identity: authenticated.Identity, PasswordChangeRequired: authenticated.PasswordChangeRequired}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal)))
	})
}

func (s *Server) Require(permission rbac.Permission, scope rbac.Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
				return
			}
			if s.rejectPasswordChangeRequired(w, r, principal) {
				return
			}
			if !s.authorizer.Allowed(principal.Identity.ID, permission, scope) {
				_ = s.audit.Append(r.Context(), audit.Event{
					Action: "authorization.check", Outcome: "denied", ActorID: principal.Identity.ID,
					SourceIP: remoteIP(r), Details: map[string]any{"permission": permission, "scope": scope},
				})
				writeError(w, r, http.StatusForbidden, "permission_denied", "Permission denied")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) RequirePasswordCurrent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		if s.rejectPasswordChangeRequired(w, r, principal) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rejectPasswordChangeRequired(w http.ResponseWriter, r *http.Request, principal Principal) bool {
	if !principal.PasswordChangeRequired {
		return false
	}
	_ = s.audit.Append(r.Context(), audit.Event{
		Action: "authorization.check", Outcome: "denied", ActorID: principal.Identity.ID,
		SourceIP: remoteIP(r), Details: map[string]any{"reason": "password_change_required"},
	})
	writeError(w, r, http.StatusForbidden, "password_change_required", "Password change is required")
	return true
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeError(w, r, http.StatusUnsupportedMediaType, "json_required", "Content-Type application/json is required")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	issued, err := s.auth.Login(r.Context(), auth.LoginInput{
		Username: input.Username, Password: input.Password, SourceIP: remoteIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "authentication_unavailable", "Authentication is temporarily unavailable")
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"session": issued.Session, "identity": issued.Identity, "password_change_required": issued.PasswordChangeRequired})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_ = s.auth.Logout(r.Context(), s.tokenFromRequest(r), remoteIP(r))
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"session": principal.Session, "password_change_required": principal.PasswordChangeRequired})
}

func (s *Server) sessionPolicy(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	policy, err := s.auth.SessionSecurityPolicy(r.Context(), auth.SessionSecurityPolicyInput{
		UserID: principal.Identity.ID, SourceIP: remoteIP(r),
	})
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "session_policy_unavailable", "Session security policy is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeError(w, r, http.StatusUnsupportedMediaType, "json_required", "Content-Type application/json is required")
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Invalid request")
		return
	}
	principal, _ := PrincipalFromContext(r.Context())
	err := s.auth.ChangePassword(r.Context(), auth.ChangePasswordInput{
		UserID: principal.Identity.ID, CurrentPassword: input.CurrentPassword,
		NewPassword: input.NewPassword, SourceIP: remoteIP(r),
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCurrentPassword):
			writeError(w, r, http.StatusUnauthorized, "invalid_current_password", "Current password is invalid")
		case errors.Is(err, auth.ErrPasswordPolicy):
			writeError(w, r, http.StatusUnprocessableEntity, "password_policy_violation", "New password does not satisfy password policy")
		case errors.Is(err, auth.ErrConflict):
			writeError(w, r, http.StatusConflict, "credentials_changed", "Credentials changed; sign in again")
		default:
			writeError(w, r, http.StatusServiceUnavailable, "password_change_unavailable", "Password change is temporarily unavailable")
		}
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	sessions, err := s.auth.ListSessions(r.Context(), auth.ListSessionsInput{
		UserID: principal.Identity.ID, CurrentSessionID: principal.Session.ID, SourceIP: remoteIP(r),
	})
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "session_inventory_unavailable", "Session inventory is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	result, err := s.auth.RevokeSession(r.Context(), auth.RevokeSessionInput{
		UserID: principal.Identity.ID, SessionID: r.PathValue("sessionID"),
		CurrentSessionID: principal.Session.ID, SourceIP: remoteIP(r),
	})
	if result.CurrentSessionRevoked {
		s.clearSessionCookie(w)
	}
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrUnauthenticated):
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
		case errors.Is(err, auth.ErrNotFound):
			writeError(w, r, http.StatusNotFound, "session_not_found", "Active session was not found")
		default:
			writeError(w, r, http.StatusServiceUnavailable, "session_revocation_unavailable", "Session revocation is temporarily unavailable")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	_, err := s.auth.RevokeAllSessions(r.Context(), auth.RevokeAllSessionsInput{
		UserID: principal.Identity.ID, SourceIP: remoteIP(r),
	})
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication is required")
			return
		}
		writeError(w, r, http.StatusServiceUnavailable, "session_revocation_unavailable", "Session revocation is temporarily unavailable")
		return
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) identitySelf(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"identity": principal.Identity})
}

func (s *Server) overviewAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "release": buildinfo.Version})
}

func (s *Server) tokenFromRequest(r *http.Request) string {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization != "" {
		parts := strings.Fields(authorization)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return parts[1]
		}
		return ""
	}
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName, Value: token, Path: "/", Expires: expires,
		MaxAge: max(1, int(time.Until(expires).Seconds())), HttpOnly: true, Secure: s.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode,
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	commonapi.WriteError(w, r, status, code, message)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center — Вход</title><style>body{font-family:system-ui;max-width:28rem;margin:8vh auto;padding:1rem}label,input,button{display:block;width:100%;box-sizing:border-box;margin:.6rem 0;padding:.7rem}</style></head>
<body><main><h1>Control Center</h1><p>Локальная учётная запись</p><form method="post" action="/web/login">
<label>Имя пользователя<input name="username" autocomplete="username" required maxlength="128"></label>
<label>Пароль<input type="password" name="password" autocomplete="current-password" required maxlength="1024"></label>
<button type="submit">Войти</button></form></main></body></html>`))

var overviewTemplate = template.Must(template.New("overview").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center</title><style>body{font-family:system-ui;max-width:54rem;margin:5vh auto;padding:1rem}.card{border:1px solid #ccc;border-radius:.7rem;padding:1rem}</style></head>
<body><main><h1>Control Center</h1><div class="card"><h2>Обзор</h2><p>Система готова. Версия {{.Version}}.</p><p>Пользователь: {{.Identity.DisplayName}} ({{.Identity.Username}})</p></div>
<form method="post" action="/web/logout"><button type="submit">Выйти</button></form></main></body></html>`))

var passwordChangeTemplate = template.Must(template.New("password-change").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Control Center — Смена пароля</title><style>body{font-family:system-ui;max-width:28rem;margin:8vh auto;padding:1rem}label,input,button{display:block;width:100%;box-sizing:border-box;margin:.6rem 0;padding:.7rem}</style></head>
<body><main><h1>Смените пароль</h1><p>До смены первоначального пароля остальные функции Control Center недоступны.</p>
<form method="post" action="/web/password/change">
<label>Текущий пароль<input type="password" name="current_password" autocomplete="current-password" required maxlength="1024"></label>
<label>Новый пароль<input type="password" name="new_password" autocomplete="new-password" required minlength="12" maxlength="1024"></label>
<button type="submit">Сменить пароль</button></form>
<form method="post" action="/web/logout"><button type="submit">Выйти</button></form></main></body></html>`))

func (s *Server) webLogin(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = loginTemplate.Execute(w, nil)
}

func (s *Server) webLoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	issued, err := s.auth.Login(r.Context(), auth.LoginInput{
		Username: r.FormValue("username"), Password: r.FormValue("password"),
		SourceIP: remoteIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		http.Error(w, "Неверное имя пользователя или пароль", http.StatusUnauthorized)
		return
	}
	s.setSessionCookie(w, issued.Token, issued.Session.ExpiresAt)
	if issued.PasswordChangeRequired {
		http.Redirect(w, r, "/password/change", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/overview", http.StatusSeeOther)
}

func (s *Server) webPasswordChange(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	if !principal.PasswordChangeRequired {
		http.Redirect(w, r, "/overview", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = passwordChangeTemplate.Execute(w, nil)
}

func (s *Server) webPasswordChangeSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	principal, _ := PrincipalFromContext(r.Context())
	err := s.auth.ChangePassword(r.Context(), auth.ChangePasswordInput{
		UserID: principal.Identity.ID, CurrentPassword: r.FormValue("current_password"),
		NewPassword: r.FormValue("new_password"), SourceIP: remoteIP(r),
	})
	if err != nil {
		http.Error(w, "Не удалось сменить пароль. Проверьте текущий пароль и требования к новому паролю.", http.StatusBadRequest)
		return
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) webOverview(w http.ResponseWriter, r *http.Request) {
	principal, _ := PrincipalFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = overviewTemplate.Execute(w, struct {
		Version  string
		Identity auth.Identity
	}{Version: buildinfo.Version, Identity: principal.Identity})
}

func (s *Server) webLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.auth.Logout(r.Context(), s.tokenFromRequest(r), remoteIP(r))
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (p Principal) String() string {
	return fmt.Sprintf("%s:%s", p.Identity.ID, p.Session.ID)
}
