package coreclient

import (
	"context"
	"net/url"

	"github.com/quickbite/analytics-service/pkg/httpclient"
)

// PermissionsByRole calls GET /api/internal/rbac/permissions?role=<role>
// and returns the flat "resource:action" list. Satisfies rbac.PermissionFetcher.
func (c *Client) PermissionsByRole(ctx context.Context, role string) ([]string, error) {
	var res Envelope[RolePermissionsResponse]
	err := c.get(ctx, httpclient.Request{
		Path:  "/api/internal/rbac/permissions",
		Query: url.Values{"role": {role}},
	}, &res)
	if err != nil {
		return nil, err
	}
	if res.Data.Permissions == nil {
		return []string{}, nil
	}
	return res.Data.Permissions, nil
}
