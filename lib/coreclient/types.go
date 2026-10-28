package coreclient

// Envelope is core-service's success envelope: {"success": true, "data": T}.
type Envelope[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

// RolePermissionsResponse is the data of GET /api/internal/rbac/permissions.
type RolePermissionsResponse struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}
