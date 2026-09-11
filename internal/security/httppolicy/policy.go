package httppolicy

type Policy struct {
	TLS            bool
	FrameAncestors string
}

func Headers(policy Policy) map[string]string {
	frameAncestors := policy.FrameAncestors
	if frameAncestors == "" {
		frameAncestors = "'none'"
	}

	headers := map[string]string{
		"Content-Security-Policy":    "default-src 'self'; frame-ancestors " + frameAncestors,
		"Referrer-Policy":            "no-referrer",
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Permissions-Policy":         "camera=(), microphone=(), geolocation=()",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	if policy.TLS {
		headers["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"
	}
	return headers
}
