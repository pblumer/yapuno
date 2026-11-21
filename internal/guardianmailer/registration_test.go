package guardianmailer

import (
	"context"
	"io"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func TestRegistrationMailCarriesTheExactLinkInGerman(t *testing.T) {
	link := "https://yapuno.example/join/verify?token=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEfG"
	raw, err := registrationMail("yapuno@example.org", "bruno@example.org", link)
	if err != nil {
		t.Fatal(err)
	}
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
	if msg.Header.Get("From") != "yapuno@example.org" || msg.Header.Get("To") != "bruno@example.org" ||
		!strings.Contains(msg.Header.Get("Subject"), "Yapuno") || msg.Header.Get("Auto-Submitted") != "auto-generated" ||
		msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatalf("headers: %v", msg.Header)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(decoded), "\r\n", "\n")
	// The link survives the encoding unchanged, alone on its line.
	if !strings.Contains(body, "\n"+link+"\n") {
		t.Fatalf("link not intact: %q", body)
	}
	for _, want := range []string{"eingeladen", "10 Minuten", "nur einmal", "ignoriere", "Familie"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q", want)
		}
	}
	if strings.Contains(body, "ß") || strings.Count(body, "https://") != 1 {
		t.Errorf("body: %q", body)
	}
}

func TestRegistrationMailRejectsUnsafeLinksAndRecipients(t *testing.T) {
	for _, link := range []string{"", "http://yapuno.example/join/verify?token=x", "https://yapuno.example/x\r\nBcc: a@example.org", "javascript:alert(1)", "https://yapuno.example/a b"} {
		if _, err := registrationMail("yapuno@example.org", "bruno@example.org", link); err == nil {
			t.Errorf("%q accepted", link)
		}
	}
	s, err := New(Config{Host: "smtp.example.org", Port: 465, Username: "u", Password: "p", From: "yapuno@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, to := range []string{"", "Bruno <bruno@example.org>", "bruno@example.org\r\nBcc: x@example.org"} {
		if err := s.SendRegistrationLink(context.Background(), to, "https://yapuno.example/join/verify?token=x"); err == nil {
			t.Errorf("recipient %q accepted", to)
		}
	}
	if err := s.SendRegistrationLink(ctx, "bruno@example.org", "https://yapuno.example/join/verify?token=x"); err == nil {
		t.Error("cancelled context ignored")
	}
	if err := s.SendRegistrationLink(context.Background(), "bruno@example.org", "http://insecure.example/"); err == nil {
		t.Error("insecure link sent")
	}
}
