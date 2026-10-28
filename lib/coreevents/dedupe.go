package coreevents

import "context"

// NoDedupe is an EventDeduper that treats every event as fresh. Use it only
// for queues whose handlers are idempotent on their own (e.g. cache
// invalidation on rbac.permissions_changed): a replay is harmless there, so a
// database write per event would be pure cost.
type NoDedupe struct{}

func (NoDedupe) MarkSeen(context.Context, string, string) (bool, error) { return true, nil }
func (NoDedupe) Forget(context.Context, string) error                   { return nil }
