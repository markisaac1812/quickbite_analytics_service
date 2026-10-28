package rbac

import (
	"context"
	"time"
)

// System roles carried in the JWT `role` claim (core-service's users.role).
const (
	RoleSystemAdmin    = "system_admin"
	RoleRestaurantUser = "restaurant_user"
)

// PermissionFetcher is the source of truth for a role's permissions.
// coreclient.Client satisfies it implicitly — lib/rbac never imports it.
type PermissionFetcher interface {
	PermissionsByRole(ctx context.Context, role string) ([]string, error)
}

type cacheEntry struct {
	perms    map[string]struct{}
	cachedAt time.Time
}

// Restaurant roles (core-service roles.name) with special meaning here.
const RestaurantRoleOwner = "owner"

// PermissionsChangedPayload is the payload of core's rbac.permissions_changed
// (same trivial shape order-service consumes). An empty Role means "all roles".
type PermissionsChangedPayload struct {
	Role string `json:"role"`
}
