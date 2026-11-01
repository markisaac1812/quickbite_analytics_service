package testkit

import (
	"context"
	"sync"
	"time"

	"github.com/quickbite/analytics-service/app/analytics"
	"github.com/quickbite/analytics-service/app/analytics/entity"
)

// FakeStores are in-memory analytics.*Store implementations for unit tests
// of the service, controller and event handlers: writes are recorded, reads
// return canned rows, and Err injects a failure into every method of a store.
// The timeline fake reproduces the real record/claim semantics so
// out-of-order scenarios can be unit-tested.
type FakeStores struct {
	Restaurants *FakeRestaurantDays
	Branches    *FakeBranchDays
	Products    *FakeProductDays
	Platform    *FakePlatformDays
	Timelines   *FakeTimelines
}

func NewFakeStores() *FakeStores {
	return &FakeStores{
		Restaurants: &FakeRestaurantDays{},
		Branches:    &FakeBranchDays{},
		Products:    &FakeProductDays{},
		Platform:    &FakePlatformDays{},
		Timelines:   &FakeTimelines{byOrder: map[string]*entity.OrderTimeline{}},
	}
}

// Stores adapts the fakes to the service's constructor argument.
func (f *FakeStores) Stores() analytics.Stores {
	return analytics.Stores{
		Restaurants: f.Restaurants, Branches: f.Branches, Products: f.Products,
		Platform: f.Platform, Timelines: f.Timelines,
	}
}

// DayInc is one recorded Increment call.
type DayInc struct {
	ID       int64 // restaurant id (restaurant store) or branch id (branch store); 0 for platform
	ParentID int64 // restaurant id on the branch store
	Date     string
	Currency string
	Delta    analytics.DayDelta
}

// RangeCall is one recorded read call.
type RangeCall struct {
	ID       int64
	ParentID int64
	From, To string
	Currency string
	Limit    int
}

// ── restaurant days ──

type FakeRestaurantDays struct {
	mu          sync.Mutex
	Err         error
	Incs        []DayInc
	Rows        []entity.RestaurantDay
	ActiveTotal int64
	ActiveDays  []entity.DateCount
	Top         []entity.RestaurantTotal
	Reads       []RangeCall
}

func (f *FakeRestaurantDays) Increment(_ context.Context, restaurantID int64, date, currency string, d analytics.DayDelta, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Incs = append(f.Incs, DayInc{ID: restaurantID, Date: date, Currency: currency, Delta: d})
	return nil
}

func (f *FakeRestaurantDays) FindRange(_ context.Context, restaurantID int64, from, to string) ([]entity.RestaurantDay, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, RangeCall{ID: restaurantID, From: from, To: to})
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Rows, nil
}

func (f *FakeRestaurantDays) ActiveRestaurants(_ context.Context, from, to string) (int64, []entity.DateCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, RangeCall{From: from, To: to})
	if f.Err != nil {
		return 0, nil, f.Err
	}
	return f.ActiveTotal, f.ActiveDays, nil
}

func (f *FakeRestaurantDays) TopRestaurants(_ context.Context, from, to, currency string, limit int) ([]entity.RestaurantTotal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, RangeCall{From: from, To: to, Currency: currency, Limit: limit})
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Top, nil
}

// ── branch days ──

type FakeBranchDays struct {
	mu    sync.Mutex
	Err   error
	Incs  []DayInc
	Rows  []entity.BranchDay
	Reads []RangeCall
}

func (f *FakeBranchDays) Increment(_ context.Context, branchID, restaurantID int64, date, currency string, d analytics.DayDelta, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Incs = append(f.Incs, DayInc{ID: branchID, ParentID: restaurantID, Date: date, Currency: currency, Delta: d})
	return nil
}

func (f *FakeBranchDays) FindRange(_ context.Context, restaurantID, branchID int64, from, to string) ([]entity.BranchDay, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, RangeCall{ID: branchID, ParentID: restaurantID, From: from, To: to})
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Rows, nil
}

// ── product days ──

// LinesCall is one recorded IncrementLines call (= one BulkWrite).
type LinesCall struct {
	RestaurantID int64
	Date         string
	Currency     string
	Lines        []analytics.OrderLine
}

type FakeProductDays struct {
	mu    sync.Mutex
	Err   error
	Bulk  []LinesCall
	Top   []entity.ProductTotal
	Reads []RangeCall
}

func (f *FakeProductDays) IncrementLines(_ context.Context, restaurantID int64, date, currency string, lines []analytics.OrderLine, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Bulk = append(f.Bulk, LinesCall{RestaurantID: restaurantID, Date: date, Currency: currency, Lines: lines})
	return nil
}

func (f *FakeProductDays) TopProducts(_ context.Context, restaurantID int64, from, to string, limit int) ([]entity.ProductTotal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, RangeCall{ID: restaurantID, From: from, To: to, Limit: limit})
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Top, nil
}

// ── platform days ──

type FakePlatformDays struct {
	mu    sync.Mutex
	Err   error
	Incs  []DayInc
	Rows  []entity.PlatformDay
	Reads []RangeCall
}

func (f *FakePlatformDays) Increment(_ context.Context, date, currency string, d analytics.DayDelta, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Incs = append(f.Incs, DayInc{Date: date, Currency: currency, Delta: d})
	return nil
}

func (f *FakePlatformDays) FindRange(_ context.Context, from, to string) ([]entity.PlatformDay, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, RangeCall{From: from, To: to})
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Rows, nil
}

// ── order timelines ──

// FakeTimelines mirrors OrderTimelineRepo: Record* upsert facts, Claim*
// returns the timeline once when both facts are known.
type FakeTimelines struct {
	mu       sync.Mutex
	Err      error // every Record*
	ClaimErr error // every Claim*
	byOrder  map[string]*entity.OrderTimeline
}

// Get returns a copy of an order's timeline (nil when unknown).
func (f *FakeTimelines) Get(orderID string) *entity.OrderTimeline {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.byOrder[orderID]
	if !ok {
		return nil
	}
	cp := *t
	return &cp
}

func (f *FakeTimelines) RecordPlaced(_ context.Context, in analytics.TimelinePlaced) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	t := f.getLocked(in.OrderID, in.RestaurantID, in.BranchID)
	t.RestaurantID, t.BranchID = in.RestaurantID, in.BranchID
	t.Currency, t.TotalMinor = in.Currency, in.TotalMinor
	placed := in.PlacedAt
	t.PlacedAt, t.PlacedDate, t.UpdatedAt = &placed, in.PlacedDate, in.At
	return nil
}

func (f *FakeTimelines) RecordDelivered(_ context.Context, orderID string, restaurantID, branchID int64, deliveredAt, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	t := f.getLocked(orderID, restaurantID, branchID)
	t.DeliveredAt, t.UpdatedAt = &deliveredAt, at
	return nil
}

func (f *FakeTimelines) RecordRejected(_ context.Context, orderID string, restaurantID, branchID int64, rejectedAt, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	t := f.getLocked(orderID, restaurantID, branchID)
	t.RejectedAt, t.UpdatedAt = &rejectedAt, at
	return nil
}

func (f *FakeTimelines) ClaimDelivery(_ context.Context, orderID string) (*entity.OrderTimeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ClaimErr != nil {
		return nil, f.ClaimErr
	}
	t, ok := f.byOrder[orderID]
	if !ok || t.PlacedAt == nil || t.DeliveredAt == nil || t.DeliveryApplied {
		return nil, nil
	}
	t.DeliveryApplied = true
	cp := *t
	return &cp, nil
}

func (f *FakeTimelines) ClaimRejection(_ context.Context, orderID string) (*entity.OrderTimeline, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ClaimErr != nil {
		return nil, f.ClaimErr
	}
	t, ok := f.byOrder[orderID]
	if !ok || t.PlacedAt == nil || t.RejectedAt == nil || t.RejectionApplied {
		return nil, nil
	}
	t.RejectionApplied = true
	cp := *t
	return &cp, nil
}

func (f *FakeTimelines) getLocked(orderID string, restaurantID, branchID int64) *entity.OrderTimeline {
	t, ok := f.byOrder[orderID]
	if !ok {
		t = &entity.OrderTimeline{OrderID: orderID, RestaurantID: restaurantID, BranchID: branchID}
		f.byOrder[orderID] = t
	}
	return t
}
