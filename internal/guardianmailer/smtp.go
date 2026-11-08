// Package guardianmailer delivers guardian login links over mandatory TLS.
package guardianmailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config requires an implicit-TLS SMTP endpoint (commonly port 465).
// STARTTLS fallback and plaintext SMTP are deliberately unsupported.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

type Sender struct{ cfg Config }

func New(cfg Config) (*Sender, error) {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil || from.Name != "" || from.Address != cfg.From || strings.ContainsAny(cfg.From, "\r\n\x00") || cfg.Host == "" || strings.ContainsAny(cfg.Host, "\r\n\x00:/@ ") || cfg.Port < 1 || cfg.Port > 65535 || cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("invalid SMTP settings")
	}
	return &Sender{cfg: cfg}, nil
}

func (s *Sender) SendSignInLink(ctx context.Context, email, link string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(email)
	if err != nil || recipient.Name != "" || recipient.Address != email || strings.ContainsAny(email, "\r\n\x00") {
		return errors.New("invalid recipient")
	}
	// Never interpolate an untrusted address into a URL; the login service
	// generates the canonical link and we require HTTPS without CR/LF.
	if !strings.HasPrefix(link, "https://") || strings.ContainsAny(link, "\r\n\x00") {
		return errors.New("invalid sign-in URL")
	}
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer raw.Close()
	conn := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.cfg.Host})
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("smtp TLS handshake failed: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp connect failed: %w", err)
	}
	defer client.Close()
	if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
		return fmt.Errorf("smtp auth failed: %w", err)
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp sender rejected: %w", err)
	}
	if err := client.Rcpt(email); err != nil {
		return fmt.Errorf("smtp recipient rejected: %w", err)
	}
	body, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp message failed: %w", err)
	}
	// ASCII only, so no RFC 2047 encoding is needed. Never print links to logs.
	message := "From: " + s.cfg.From + "\r\nTo: " + email + "\r\nSubject: Yapuno sign-in link\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n\r\n" +
		"Your one-time Yapuno sign-in link (valid for 10 minutes):\r\n" + link + "\r\n\r\n" +
		"If you did not request this, ignore this message.\r\n"
	if _, err := io.WriteString(body, message); err != nil {
		_ = body.Close()
		return fmt.Errorf("smtp write failed: %w", err)
	}
	if err := body.Close(); err != nil {
		return fmt.Errorf("smtp deliver failed: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp close failed: %w", err)
	}
	return nil
}
