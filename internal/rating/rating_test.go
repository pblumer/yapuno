package rating

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	lea  = Player{Kind: Child, TenantID: "family-a", ID: "lea"}
	mia  = Player{Kind: Child, TenantID: "family-b", ID: "mia"}
	anna = Player{Kind: Guardian, TenantID: "family-a", ID: "anna"}
	easy = Computer("easy")
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path, func() time.Time { return time.Date(2026, 11, 28, 17, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExpected(t *testing.T) {
	if e := expected(1000, 1000); e != 0.5 {
		t.Errorf("equal: %v", e)
	}
	if e := expected(1400, 1000); math.Abs(e-0.909) > 0.001 {
		t.Errorf("400 points: %v", e)
	}
}

func TestRecordUpdatesBothSidesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratings.json")
	s := open(t, path)
	if r := s.Of(lea); r.Value != Start || r.Games != 0 || !r.Provisional {
		t.Fatalf("new player: %+v", r)
	}
	if err := s.Record("game:1", lea, mia, 1); err != nil {
		t.Fatal(err)
	}
	if r := s.Of(lea); r.Value != Start+K/2 || r.Games != 1 {
		t.Fatalf("winner: %+v", r)
	}
	if r := s.Of(mia); r.Value != Start-K/2 || r.Games != 1 {
		t.Fatalf("loser: %+v", r)
	}
	// The same game again changes nothing, also after a restart.
	if err := s.Record("game:1", lea, mia, 1); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	if err := s.Record("game:1", lea, mia, 0); err != nil {
		t.Fatal(err)
	}
	if r := s.Of(lea); r.Value != Start+K/2 || r.Games != 1 {
		t.Fatalf("idempotent: %+v", r)
	}
	// A draw against a guardian, who is a fixed 1000, counts for the child
	// and stores nothing about the guardian.
	if err := s.Record("game:2", anna, mia, 0.5); err != nil {
		t.Fatal(err)
	}
	if r := s.Of(anna); r.Value != Start || r.Games != 0 || r.Provisional {
		t.Fatalf("guardian: %+v", r)
	}
	if r := s.Of(mia); r.Games != 2 || r.Value <= Start-K/2 {
		t.Fatalf("child after draw against 1000: %+v", r)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"guardian"`) {
		t.Fatalf("guardian stored: %s", data)
	}
	// A game without a child rates nobody.
	if err := s.Record("game:3", anna, Player{Kind: Guardian, TenantID: "family-b", ID: "bruno"}, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("two guardians: %v", err)
	}
	if err := s.Record("game:4", anna, easy, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("guardian against computer: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestComputerIsAnAnchor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratings.json")
	s := open(t, path)
	if err := s.Record("practice:1", lea, easy, 1); err != nil {
		t.Fatal(err)
	}
	// Beating a 600 player from 1000 earns little.
	if r := s.Of(lea); r.Value <= Start || r.Value >= Start+5 {
		t.Fatalf("against easy: %+v", r)
	}
	if r := s.Of(easy); r.Value != 600 || r.Games != 0 || r.Provisional {
		t.Fatalf("computer changed: %+v", r)
	}
	// The computer is never written, so the file opens again.
	s.Record("practice:2", easy, lea, 0)
	s = open(t, path)
	if s.Of(lea).Games != 2 {
		t.Fatal("games after reopen")
	}
	if err := s.Record("x", lea, Computer("godlike"), 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown level: %v", err)
	}
	for _, bad := range []struct {
		a, b  Player
		score float64
	}{{lea, lea, 1}, {lea, mia, 0.7}, {Player{Kind: Child}, mia, 1}, {lea, Player{Kind: "alien", ID: "x"}, 1}} {
		if err := s.Record("bad", bad.a, bad.b, bad.score); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestProvisionalAndPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratings.json")
	s := open(t, path)
	for i := 0; i < Provisional; i++ {
		s.Record("g"+string(rune('a'+i)), lea, mia, 0.5)
	}
	if r := s.Of(lea); r.Provisional || r.Games != Provisional {
		t.Fatalf("after %d games: %+v", Provisional, r)
	}
	s.Record("h", anna, lea, 1)
	sameID := Player{Kind: Guardian, TenantID: "family-a", ID: "lea"}
	s.Record("i", sameID, mia, 1)
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	if r := s.Of(lea); r.Games != 0 || r.Value != Start {
		t.Fatalf("purge child: %+v", r)
	}
	if r := s.Of(mia); r.Games != Provisional+1 {
		t.Fatalf("purge child touched another child: %+v", r)
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	if s.Of(mia).Games != Provisional+1 {
		t.Fatal("purge tenant")
	}
}

// Files written before guardians became a fixed anchor may hold guardian
// rows. They are dropped on load and gone from the file at the next write.
func TestLegacyGuardianRowsAreDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratings.json")
	legacy := `{"players":[{"kind":"guardian","tenant_id":"family-a","id":"anna","rating":1100,"games":3},{"kind":"child","tenant_id":"family-a","id":"lea","rating":1040,"games":3}],"recorded":[]}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	s := open(t, path)
	if r := s.Of(anna); r.Value != Start || r.Games != 0 {
		t.Fatalf("legacy guardian row used: %+v", r)
	}
	if r := s.Of(lea); r.Value != 1040 {
		t.Fatalf("child row lost: %+v", r)
	}
	s.Record("game:new", lea, mia, 1)
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "anna") {
		t.Fatalf("legacy guardian row kept: %s", data)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		content string
		mode    os.FileMode
	}{
		"world readable": {`{"players":[],"recorded":[]}`, 0644},
		"unknown field":  {`{"players":[],"recorded":[],"x":1}`, 0600},
		"computer row":   {`{"players":[{"kind":"computer","tenant_id":"","id":"easy","rating":1,"games":1}],"recorded":[]}`, 0600},
		"no id":          {`{"players":[{"kind":"child","tenant_id":"t","id":"","rating":1000,"games":1}],"recorded":[]}`, 0600},
		"corrupt":        {`{"players":`, 0600},
	} {
		p := filepath.Join(dir, name+".json")
		os.WriteFile(p, []byte(c.content), c.mode)
		os.Chmod(p, c.mode)
		if _, err := Open(p, time.Now); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}
