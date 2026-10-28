package rbac

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/quickbite/analytics-service/lib/coreevents"
	"github.com/quickbite/analytics-service/lib/logger"
)

// EventPermissionsChanged is core-service's routing key (core lib/events/event-types.ts).
const EventPermissionsChanged = "rbac.permissions_changed"

// HandlePermissionsChanged drops the changed role (or every role) from the
// cache so the next request re-fetches from core. Idempotent, so its queue
// needs no dedupe store. Go analogue of order-service's
// PermissionCacheService.handlePermissionsChanged.
func (c *Cache) HandlePermissionsChanged(ctx context.Context, env coreevents.Envelope) error {
	var p PermissionsChangedPayload
	if len(env.Payload) > 0 && string(env.Payload) != "null" {
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return fmt.Errorf("rbac.permissions_changed payload: %w", err)
		}
	}
	c.Invalidate(p.Role)

	role := p.Role
	if role == "" {
		role = "*"
	}
	logger.FromContext(ctx).Info("rbac.permissions_changed -> permission cache cleared", "role", role)
	return nil
}
