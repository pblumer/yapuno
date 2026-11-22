package contacts

import (
	"testing"
)

func TestConnectionsListEstablishedContactsIncludingPausedOnes(t *testing.T) {
	s := setup(t).store
	a, b, c := lea, mia, noah
	if got := s.Connections(a); len(got) != 0 {
		t.Fatalf("connections before any contact: %+v", got)
	}
	code, err := s.Invite("anna", "family-a", "lea")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Redeem("bruno", "family-b", "mia", code)
	if err != nil {
		t.Fatal(err)
	}
	// A request that is not confirmed yet connects nobody.
	if len(s.Connections(a)) != 0 || len(s.Connections(b)) != 0 {
		t.Fatal("a pending request counts as a connection")
	}
	if err := s.Confirm("anna", "family-a", "lea", id); err != nil {
		t.Fatal(err)
	}
	got := s.Connections(a)
	if len(got) != 1 || got[0].Peer != b || !got[0].Grant.Active || !got[0].Grant.FirstApproved || !got[0].Grant.SecondApproved || got[0].Grant.Blocked {
		t.Fatalf("connections of a: %+v", got)
	}
	if back := s.Connections(b); len(back) != 1 || back[0].Peer != a {
		t.Fatalf("connections of b: %+v", back)
	}
	if len(s.Connections(c)) != 0 || len(s.Connections(tim)) != 0 || len(s.Connections(Party{TenantID: "family-b", ChildID: "lea"})) != 0 || len(s.Connections(Party{})) != 0 {
		t.Fatal("unrelated or unknown children have connections")
	}
	// A block pauses the children's chat; the connection is still reported, marked as blocked.
	if err := s.Block(b, id); err != nil {
		t.Fatal(err)
	}
	if got := s.Connections(a); len(got) != 1 || !got[0].Grant.Blocked || len(s.ContactsOf(a)) != 0 {
		t.Fatalf("blocked connection: %+v", got)
	}
	// Ending the contact ends the connection.
	if err := s.Remove("anna", "family-a", "lea", id); err != nil {
		t.Fatal(err)
	}
	if len(s.Connections(a)) != 0 || len(s.Connections(b)) != 0 {
		t.Fatal("an ended contact still connects")
	}
}
