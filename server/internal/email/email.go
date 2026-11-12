// Package email is the outbound email adapter (T27): a tiny Sender seam
// with an SMTP implementation. Delivery is at-least-once by design — a
// crash between a successful send and the database ack redelivers; the
// stable per-intent Message-ID lets a receiver dedupe on a best-effort
// basis (not an exactly-once promise). No secrets or media ever ride in
// digest bodies: they are composed from plain-text care titles upstream.
package email

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is one plain-text email. MessageID is stable per notification
// intent so retries keep the same identity.
type Message struct {
	To        string
	Subject   string
	Text      string
	MessageID string // bare id, e.g. "abc@canopy"; angle brackets added on the wire
}

// Sender delivers one message. Implementations classify failures with
// short stable codes (surfaced in delivery_attempts.error_code).
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// DeliveryError carries a stable, short code for the attempt log.
type DeliveryError struct {
	Code string // dial | auth | mail | rcpt | data
	Err  error
}

func (e *DeliveryError) Error() string { return "email " + e.Code + ": " + e.Err.Error() }
func (e *DeliveryError) Unwrap() error { return e.Err }

// SMTPSender talks SMTP directly (stdlib): STARTTLS on a submission
// port, optional PLAIN auth. Empty Username means unauthenticated
// (local relay) — self-hosted single-owner deployments often run one.
type SMTPSender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // envelope + header sender
	Hello    string // EHLO name; defaults to the dial host
	Timeout  time.Duration
}

func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if m.To == "" {
		return &DeliveryError{Code: "rcpt", Err: fmt.Errorf("no owner recipient configured")}
	}
	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	d := net.Dialer{Timeout: s.Timeout}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return &DeliveryError{Code: "dial", Err: err}
	}
	c, err := smtp.NewClient(nc, s.Host)
	if err != nil {
		return &DeliveryError{Code: "dial", Err: err}
	}
	defer c.Close()
	if hello := s.Hello; hello != "" {
		if err := c.Hello(hello); err != nil {
			return &DeliveryError{Code: "dial", Err: err}
		}
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(nil); err != nil {
			return &DeliveryError{Code: "dial", Err: err}
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
				return &DeliveryError{Code: "auth", Err: err}
			}
		}
	}
	if err := c.Mail(s.From); err != nil {
		return &DeliveryError{Code: "mail", Err: err}
	}
	if err := c.Rcpt(m.To); err != nil {
		return &DeliveryError{Code: "rcpt", Err: err}
	}
	w, err := c.Data()
	if err != nil {
		return &DeliveryError{Code: "data", Err: err}
	}
	if _, err := w.Write([]byte(wireMessage(s.From, m))); err != nil {
		return &DeliveryError{Code: "data", Err: err}
	}
	if err := w.Close(); err != nil {
		return &DeliveryError{Code: "data", Err: err}
	}
	return c.Quit()
}

// wireMessage renders a minimal RFC 5322 plain-text message. Subject is
// stripped of CR/LF so a title can never inject headers.
func wireMessage(from string, m Message) string {
	subj := strings.NewReplacer("\r", " ", "\n", " ").Replace(m.Subject)
	var b strings.Builder
	fmt.Fprintf(&b, "Message-ID: <%s>\r\n", m.MessageID)
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", subj)
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(m.Text)
	if !strings.HasSuffix(m.Text, "\n") {
		b.WriteString("\r\n")
	}
	return b.String()
}
