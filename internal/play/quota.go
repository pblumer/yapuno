// Package play defines server-authoritative, child-scoped gaming quota policy.
// This package does not persist balances or authenticate players.
package play

import (
	"errors"
	"time"
)

var (
	ErrInvalidPolicy = errors.New("invalid play quota policy")
	ErrNotAllowed = errors.New("game not allowed")
	ErrExhausted = errors.New("play quota exhausted")
)

// Policy is configured by an authorized guardian and loaded from trusted storage.
// DailyLimitSeconds is shared across all games and devices for one child.
type Policy struct {
	ChildID string
	TenantID string
	DailyLimitSeconds int64
	AllowedGames map[string]bool
	AllowedWeekdays [7]bool // Sunday=0, Saturday=6 in the child's configured timezone.
}

// Usage is a committed server-side daily snapshot, not a client-reported timer.
type Usage struct {
	ChildID string
	TenantID string
	Day string // YYYY-MM-DD in the child's configured timezone.
	ConsumedSeconds int64
}

// Remaining evaluates a quota snapshot. The caller must atomically reserve or
// charge usage in persistence before starting/continuing a session. Concurrent
// sessions cannot safely use this pure function as a balance lock.
func Remaining(policy Policy, usage Usage, gameID string, now time.Time) (int64, error) {
	if policy.ChildID == "" || policy.TenantID == "" || policy.DailyLimitSeconds < 0 ||
		usage.ChildID != policy.ChildID || usage.TenantID != policy.TenantID ||
		usage.ConsumedSeconds < 0 || usage.Day != now.Format("2006-01-02") {
		return 0, ErrInvalidPolicy
	}
	if gameID == "" || !policy.AllowedGames[gameID] || !policy.AllowedWeekdays[int(now.Weekday())] {
		return 0, ErrNotAllowed
	}
	if usage.ConsumedSeconds >= policy.DailyLimitSeconds {
		return 0, ErrExhausted
	}
	return policy.DailyLimitSeconds - usage.ConsumedSeconds, nil
}
