package httpapi

import (
	"io"

	productui "control-center/internal/ui"
)

// RenderInfrastructureInventory renders the authenticated 0.30 read-only
// Sites/Nodes/Inventory page. Authentication and RBAC remain the caller's
// responsibility so the renderer can be composed with the existing identity
// middleware without coupling the UI model back to identity internals.
func RenderInfrastructureInventory(w io.Writer, version, displayName, username string, inventory productui.InfrastructureInventory) error {
	return renderInfrastructureInventory(w, version, displayName, username, inventory)
}
