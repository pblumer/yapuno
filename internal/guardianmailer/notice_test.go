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

func TestReportNoticeNamesOnlyTheOwnChild(t *testing.T) {
	raw := reportNotice("yapuno@example.org", "anna@example.org", "Zoë")
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
	if msg.Header.Get("From") != "yapuno@example.org" || msg.Header.Get("To") != "anna@example.org" || msg.Header.Get("Auto-Submitted") != "auto-generated" {
		t.Fatalf("headers: %v", msg.Header)
	}
	// The subject shows on lock screens: it names nobody.
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || !strings.Contains(subject, "Yapuno") || strings.Contains(subject, "Zoë") {
		t.Fatalf("subject: %q %v", subject, err)
	}
	if msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatalf("content headers: %v", msg.Header)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	body := string(decoded)
	for _, want := range []string{"Zoë hat in Yapuno eine Nachricht gemeldet", "Kontakte von Zoë", "keinen Link", "andere Familie", "eine solche E-Mail pro Stunde"} {
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

func TestReportNoticeNeutralizesOddChildNames(t *testing.T) {
	for name, child := range map[string]string{"empty": "", "control characters": "Al\x1bex", "tab": "Al\tex", "too long": strings.Repeat("x", 200), "header injection": "Alex\r\nBcc: evil@example.org"} {
		raw := reportNotice("yapuno@example.org", "anna@example.org", child)
		msg, err := mail.ReadMessage(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if msg.Header.Get("Bcc") != "" || len(msg.Header) != 7 {
			t.Fatalf("%s: headers changed: %v", name, msg.Header)
		}
		decoded, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
		if !strings.Contains(string(decoded), "Dein Kind hat in Yapuno eine Nachricht gemeldet") || strings.Contains(string(decoded), "evil") {
			t.Errorf("%s: odd name not replaced: %q", name, string(decoded)[:80])
		}
	}
}

func TestReportNoticeRejectsBadRecipients(t *testing.T) {
	s, err := New(Config{Host: "smtp.example.org", Port: 465, Username: "login", Password: "secret", From: "login@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"victim@example.org\r\nBcc:attacker@example.org", "No Name <victim@example.org>", "garbage", ""} {
		if err := s.SendReportNotice(context.Background(), email, "Alex"); err == nil {
			t.Fatalf("accepted malformed recipient: %q", email)
		}
	}
}

func TestReportReminderSaysNobodyLookedAndNamesOnlyTheOwnChild(t *testing.T) {
	raw := reportReminder("yapuno@example.org", "anna@example.org", "Zoë")
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
	if msg.Header.Get("To") != "anna@example.org" || msg.Header.Get("Auto-Submitted") != "auto-generated" || len(msg.Header) != 7 {
		t.Fatalf("headers: %v", msg.Header)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || !strings.Contains(subject, "wartet noch") || strings.Contains(subject, "Zoë") {
		t.Fatalf("subject: %q %v", subject, err)
	}
	if subject == "Yapuno: Eine Meldung wartet auf dich" {
		t.Fatal("reminder must be distinguishable from the first notice")
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	body := string(decoded)
	for _, want := range []string{"Zoë hat in Yapuno eine Nachricht gemeldet", "noch nicht angesehen", "Kontakte von Zoë", "keinen Link", "höchstens zwei Erinnerungen"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q", want)
		}
	}
	for _, absent := range []string{"http://", "https://", "www.", "@", "ß"} {
		if strings.Contains(body, absent) {
			t.Errorf("body contains %q", absent)
		}
	}
	odd, _ := io.ReadAll(quotedprintable.NewReader(mustBody(t, reportReminder("yapuno@example.org", "anna@example.org", "Alex\r\nBcc: evil@example.org"))))
	if !strings.Contains(string(odd), "Dein Kind hat in Yapuno eine Nachricht gemeldet") || strings.Contains(string(odd), "evil") {
		t.Fatal("odd child name not replaced in the reminder")
	}
	s, err := New(Config{Host: "smtp.example.org", Port: 465, Username: "login", Password: "secret", From: "login@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendReportReminder(context.Background(), "No Name <victim@example.org>", "Alex"); err == nil {
		t.Fatal("accepted malformed recipient")
	}
}

func mustBody(t *testing.T, raw string) io.Reader {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return msg.Body
}
