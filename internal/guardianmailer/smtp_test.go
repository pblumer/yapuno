package guardianmailer

import (
	"context"
	"testing"
)

func TestSMTPValidatesSettingsAndRejectsInjection(t *testing.T) {
	cfg := Config{Host: "smtp.example.org", Port: 465, Username: "login", Password: "secret", From: "login@example.org"}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"victim@example.org\r\nBcc:attacker@example.org", "No Name <victim@example.org>", "garbage"} {
		if err := s.SendSignInLink(context.Background(), email, "https://app.example.org/login/verify?token=x"); err == nil {
			t.Fatalf("accepted malformed recipient: %q", email)
		}
	}
	if err := s.SendSignInLink(context.Background(), "victim@example.org", "http://app.example.org/token"); err == nil {
		t.Fatal("accepted insecure link")
	}
	cfg.From = "someone@example.org\r\nBcc:attacker@example.org"
	if _, err := New(cfg); err == nil {
		t.Fatal("header injection from")
	}
	cfg.From = "login@example.org"
	cfg.Host = "smtp.example.org:25"
	if _, err := New(cfg); err == nil {
		t.Fatal("host injection")
	}
	cfg.Host = "smtp.example.org"
	cfg.Password = ""
	if _, err := New(cfg); err == nil {
		t.Fatal("missing authentication accepted")
	}
}
