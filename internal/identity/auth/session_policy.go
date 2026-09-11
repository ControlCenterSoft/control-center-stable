package auth

import (
	"context"
	"time"

	"control-center/internal/identity/audit"
)

type SessionSecurityPolicyView struct {
	AbsoluteTTLSeconds            int64 `json:"absolute_ttl_seconds"`
	IdleTimeoutSeconds            int64 `json:"idle_timeout_seconds"`
	ActivityRefreshesIdleDeadline bool  `json:"activity_refreshes_idle_deadline"`
	ActivityExtendsAbsoluteExpiry bool  `json:"activity_extends_absolute_expiry"`
}

type SessionSecurityPolicyInput struct {
	UserID   string
	SourceIP string
}

func (s *Service) SessionSecurityPolicy(ctx context.Context, input SessionSecurityPolicyInput) (SessionSecurityPolicyView, error) {
	user, err := s.users.FindUserByID(ctx, input.UserID)
	if err != nil || !user.Enabled {
		return SessionSecurityPolicyView{}, ErrUnauthenticated
	}

	view := SessionSecurityPolicyView{
		AbsoluteTTLSeconds:            durationSeconds(s.sessionTTL),
		IdleTimeoutSeconds:            durationSeconds(s.sessionIdleTimeout),
		ActivityRefreshesIdleDeadline: true,
		ActivityExtendsAbsoluteExpiry: false,
	}
	if err := s.audit.Append(ctx, audit.Event{
		Action:    "auth.session_policy_read",
		Outcome:   "success",
		ActorID:   user.ID,
		SubjectID: user.ID,
		SourceIP:  input.SourceIP,
		Details: map[string]any{
			"absolute_ttl_seconds": view.AbsoluteTTLSeconds,
			"idle_timeout_seconds": view.IdleTimeoutSeconds,
		},
	}); err != nil {
		return SessionSecurityPolicyView{}, ErrAuditUnavailable
	}
	return view, nil
}

func durationSeconds(value time.Duration) int64 {
	return int64(value / time.Second)
}
