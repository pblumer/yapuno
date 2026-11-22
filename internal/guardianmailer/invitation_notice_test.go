package guardianmailer

import (
	"context"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func TestInvitationContactNoticeNamesOnlyTheOwnChildAndCarriesNoLink(t *testing.T) {
	raw := invitationContactNotice("yapuno@example.org", "anna@example.org", "Zoë")
	if rest := strings.ReplaceAll(raw, "\r\n", ""); strings.ContainsAny(rest, "\r\n") {
		t.Fatal("bare CR or LF in message")
	}
	for _, line := range strings.Split(raw, "\r\n") {
		if len(line) > 78 {
			t.Fatalf("line longer than 78 bytes: %q", line)
		}
		for _, b := range []byte(line) {
			if b > 126 {
				t.Fatalf("non-ASCII byte on the wire: %q", line)
			}
		}
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("From") != "yapuno@example.org" || msg.Header.Get("To") != "anna@example.org" || msg.Header.Get("Auto-Submitted") != "auto-generated" ||
		msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatalf("headers: %v", msg.Header)
	}
	// The subject shows on lock screens: it names nobody.
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || !strings.Contains(subject, "Yapuno") || !strings.Contains(subject, "Kontakt") || strings.Contains(subject, "Zoë") {
		t.Fatalf("subject: %q %v", subject, err)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	body := string(decoded)
	for _, want := range []string{"Einladung", "Zoë hat dadurch einen neuen Kontakt", "Kontakte von Zoë", "keinen Link", "beende den Kontakt"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q", want)
		}
	}
	for _, absent := range []string{"http://", "https://", "www.", "@", "ß"} {
		if strings.Contains(body, absent) {
			t.Errorf("body contains %q", absent)
		}
	}
}

func TestInvitationContactNoticeNeutralizesOddChildNames(t *testing.T) {
	for name, child := range map[string]string{"empty": "", "control characters": "Al\x1bex", "too long": strings.Repeat("x", 200), "header injection": "Alex\r\nBcc: evil@example.org"} {
		raw := invitationContactNotice("yapuno@example.org", "anna@example.org", child)
		msg, err := mail.ReadMessage(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if msg.Header.Get("Bcc") != "" || len(msg.Header) != 7 {
			t.Fatalf("%s: headers changed: %v", name, msg.Header)
		}
		decoded, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
		if !strings.Contains(string(decoded), "Dein Kind hat dadurch einen neuen Kontakt") || !strings.Contains(string(decoded), "Kontakte von deinem Kind") || strings.Contains(string(decoded), "evil") {
			t.Errorf("%s: odd name not replaced: %q", name, string(decoded))
		}
	}
}

func TestInvitationContactNoticeRejectsBadRecipients(t *testing.T) {
	s, err := New(Config{Host: "smtp.example.org", Port: 465, Username: "login", Password: "secret", From: "login@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"victim@example.org\r\nBcc:attacker@example.org", "No Name <victim@example.org>", "garbage", ""} {
		if err := s.SendInvitationContactNotice(context.Background(), email, "Alex"); err == nil {
			t.Fatalf("accepted malformed recipient: %q", email)
		}
	}
}
