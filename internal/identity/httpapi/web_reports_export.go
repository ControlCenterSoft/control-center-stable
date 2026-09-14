package httpapi

import (
	"io"

	productui "control-center/internal/ui"
)

// RenderOperationalReport renders a read-only authenticated Reports page.
// Authentication and source-specific RBAC remain the caller's responsibility.
func RenderOperationalReport(w io.Writer, version, displayName, username, sourceLabel string, report productui.OperationalReport) error {
	return renderOperationalReport(w, version, displayName, username, sourceLabel, report)
}
