package rating

import (
	"errors"
	"math"
	"os"
	"path/filepath"
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
	// A draw between equals changes nothing but counts.
	s.Record("game:2", anna, Player{Kind: Guardian, TenantID: "family-b", ID: "bruno"}, 0.5)
	if r := s.Of(anna); r.Value != Start || r.Games != 1 {
		t.Fatalf("draw: %+v", r)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestComputerIsAnAnchor(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "ratings.json"))
	if err := s.Record("practice:1", lea, easy, 1); err != nil {
		t.Fatal(err)
	}
	// Beating a 600 player from 1000 earns little.
	if r := s.Of(lea); r.Value <= Start || r.Value >= Start+5 {
		t.Fatalf("against easy: %+v", r)
	}
	if r := s.Of(easy); r.Value != 600 || r.Games != 0 {
		t.Fatalf("computer changed: %+v", r)
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
	if err := s.PurgeChild("family-a", "lea"); err != nil {
		t.Fatal(err)
	}
	if r := s.Of(lea); r.Games != 0 || r.Value != Start {
		t.Fatalf("purge child: %+v", r)
	}
	if r := s.Of(anna); r.Games != 1 {
		t.Fatal("purge child removed the guardian")
	}
	if err := s.PurgeTenant("family-a"); err != nil {
		t.Fatal(err)
	}
	s = open(t, path)
	if s.Of(anna).Games != 0 || s.Of(mia).Games != Provisional {
		t.Fatal("purge tenant")
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
