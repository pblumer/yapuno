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

func TestAccessLockNoticeIsWellFormedGermanMailWithoutLinks(t *testing.T) {
	raw := accessLockNotice("yapuno@example.org", "ben@example.org")
	if strings.Contains(strings.ReplaceAll(raw, "\r\n", ""), "\n") || strings.Contains(strings.ReplaceAll(raw, "\r\n", ""), "\r") {
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
	if msg.Header.Get("From") != "yapuno@example.org" || msg.Header.Get("To") != "ben@example.org" {
		t.Fatalf("addresses: %v", msg.Header)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || !strings.Contains(subject, "Yapuno") || !strings.Contains(subject, "gesperrt") {
		t.Fatalf("subject: %q %v", subject, err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/plain" || !strings.EqualFold(params["charset"], "utf-8") || msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatalf("content headers: %v", msg.Header)
	}
	// Automatic replies and vacation responders should stay quiet.
	if msg.Header.Get("Auto-Submitted") != "auto-generated" {
		t.Fatal("notice not marked as automatic")
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	body := string(decoded)
	for _, want := range []string{"Elternteil", "gesperrt", "Geräte", "Kontakte", "Gerät verloren", "Betreiber", "keinen Link"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q", want)
		}
	}
	// No link to click, no address of the locking guardian, no eszett.
	for _, absent := range []string{"http://", "https://", "www.", "@", "ß"} {
		if strings.Contains(body, absent) {
			t.Errorf("body contains %q", absent)
		}
	}
}

func TestAccessLockNoticeRejectsBadRecipients(t *testing.T) {
	s, err := New(Config{Host: "smtp.example.org", Port: 465, Username: "login", Password: "secret", From: "login@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"victim@example.org\r\nBcc:attacker@example.org", "No Name <victim@example.org>", "garbage", ""} {
		if err := s.SendAccessLockNotice(context.Background(), email); err == nil {
			t.Fatalf("accepted malformed recipient: %q", email)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SendAccessLockNotice(cancelled, "ben@example.org"); err == nil {
		t.Fatal("cancelled context accepted")
	}
}
