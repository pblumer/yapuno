package play

import (
	"errors"
	"testing"
	"time"
)

func TestRemaining(t *testing.T) {
	now := time.Date(2026, 11, 4, 18, 0, 0, 0, time.FixedZone("CET", 3600))
	policy := Policy{ChildID: "child-1", TenantID: "family-1", DailyLimitSeconds: 1800, AllowedGames: map[string]bool{"chess": true}}
	policy.AllowedWeekdays[int(now.Weekday())] = true
	usage := Usage{ChildID: "child-1", TenantID: "family-1", Day: "2026-11-04", ConsumedSeconds: 600}
	tests := []struct {
		name   string
		change func(*Policy, *Usage, *string, *time.Time)
		want   int64
		err    error
	}{
		{"remaining", func(*Policy, *Usage, *string, *time.Time) {}, 1200, nil},
		{"exhausted", func(_ *Policy, u *Usage, _ *string, _ *time.Time) { u.ConsumedSeconds = 1800 }, 0, ErrExhausted},
		{"disabled game", func(_ *Policy, _ *Usage, g *string, _ *time.Time) { *g = "platform" }, 0, ErrNotAllowed},
		{"other child", func(_ *Policy, u *Usage, _ *string, _ *time.Time) { u.ChildID = "child-2" }, 0, ErrInvalidPolicy},
		{"other tenant", func(_ *Policy, u *Usage, _ *string, _ *time.Time) { u.TenantID = "other" }, 0, ErrInvalidPolicy},
		{"negative usage", func(_ *Policy, u *Usage, _ *string, _ *time.Time) { u.ConsumedSeconds = -1 }, 0, ErrInvalidPolicy},
		{"day mismatch", func(_ *Policy, u *Usage, _ *string, _ *time.Time) { u.Day = "2026-11-03" }, 0, ErrInvalidPolicy},
		{"zero limit", func(p *Policy, _ *Usage, _ *string, _ *time.Time) { p.DailyLimitSeconds = 0 }, 0, ErrExhausted},
		{"negative limit", func(p *Policy, _ *Usage, _ *string, _ *time.Time) { p.DailyLimitSeconds = -1 }, 0, ErrInvalidPolicy},
		{"weekday disabled", func(p *Policy, _ *Usage, _ *string, _ *time.Time) { p.AllowedWeekdays[int(now.Weekday())] = false }, 0, ErrNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, u, game, at := policy, usage, "chess", now
			tt.change(&p, &u, &game, &at)
			got, err := Remaining(p, u, game, at)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Fatalf("remaining = %d, %v; want %d, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
