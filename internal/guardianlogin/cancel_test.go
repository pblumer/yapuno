package guardianlogin

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type manyGuardians map[string]Principal

func (m manyGuardians) LookupGuardian(_ context.Context, email string) (Principal, error) {
	return m[email], nil
}

func TestCancelInvalidatesUnusedLinksOfExactlyThatGuardian(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	dir := manyGuardians{
		"a@example.org":  {GuardianID: "g-a", TenantID: "t-1", Email: "a@example.org", Active: true},
		"b@example.org":  {GuardianID: "g-b", TenantID: "t-1", Email: "b@example.org", Active: true},
		"a2@example.org": {GuardianID: "g-a", TenantID: "t-2", Email: "a2@example.org", Active: true},
	}
	mail, sessions := &sender{}, &sessionIssuer{}
	h, err := New(Config{Origin: "https://yapuno.example.org", Directory: dir, Sender: mail, Sessions: sessions, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"a@example.org", "b@example.org", "a2@example.org"} {
		start(h, address)
	}
	// A second link for the same guardian after the cooldown.
	now = now.Add(2 * time.Minute)
	start(h, "a@example.org")
	if len(mail.links) != 4 {
		t.Fatalf("links sent: %d", len(mail.links))
	}
	if got := h.Cancel("g-a", "t-1"); got != 2 {
		t.Fatalf("cancelled: %d", got)
	}
	for _, i := range []int{0, 3} {
		if code := verify(h, tokenFromLink(t, mail.links[i])).Code; code != http.StatusForbidden {
			t.Fatalf("cancelled link %d accepted: %d", i, code)
		}
	}
	if sessions.calls() != 0 {
		t.Fatal("a cancelled link issued a session")
	}
	for _, i := range []int{1, 2} {
		if code := verify(h, tokenFromLink(t, mail.links[i])).Code; code != http.StatusSeeOther {
			t.Fatalf("link %d of another guardian was cancelled: %d", i, code)
		}
	}
	if h.Cancel("g-a", "t-1") != 0 || h.Cancel("", "t-1") != 0 || h.Cancel("g-b", "") != 0 {
		t.Fatal("repeated or empty cancel must change nothing")
	}
	// Cancelling is not a lock: a fresh link works.
	now = now.Add(2 * time.Minute)
	start(h, "a@example.org")
	if code := verify(h, tokenFromLink(t, mail.links[len(mail.links)-1])).Code; code != http.StatusSeeOther {
		t.Fatalf("fresh link after cancel: %d", code)
	}
}
