// Package email provides transactional email delivery via SMTP or AWS SES v2.
//
// SMTP is preferred when SMTP_HOST is set, which keeps local development simple
// with tools like Mailpit. Otherwise SES is used when EMAIL_FROM is set.
//
// Relevant env vars:
//   - EMAIL_FROM      — sender address, e.g. "Hizal <noreply@winnow-api.xferops.dev>"
//   - SMTP_HOST       — SMTP hostname for local/dev delivery
//   - SMTP_PORT       — SMTP port (defaults to 25)
//   - SMTP_USERNAME   — optional SMTP username
//   - SMTP_PASSWORD   — optional SMTP password
//   - AWS_REGION      — used by the default AWS config loader for SES
//
// If EMAIL_FROM is unset, Send() returns nil without sending.
package email

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// Client wraps SES v2.
type Client struct {
	ses          *sesv2.Client
	from         string
	envelopeFrom string
	smtpAddr     string
	smtpAuth     smtp.Auth
}

// New creates a Client from the ambient environment configuration.
// Returns (nil, nil) when EMAIL_FROM is not set — callers should treat nil as a no-op.
func New(ctx context.Context) (*Client, error) {
	from := os.Getenv("EMAIL_FROM")
	if from == "" {
		return nil, nil
	}

	envelopeFrom, err := parseEnvelopeFrom(from)
	if err != nil {
		return nil, fmt.Errorf("email: parse EMAIL_FROM: %w", err)
	}

	if host := os.Getenv("SMTP_HOST"); host != "" {
		port := os.Getenv("SMTP_PORT")
		if port == "" {
			port = "25"
		}

		var auth smtp.Auth
		username := os.Getenv("SMTP_USERNAME")
		password := os.Getenv("SMTP_PASSWORD")
		if username != "" {
			auth = smtp.PlainAuth("", username, password, host)
		}

		return &Client{
			from:         from,
			envelopeFrom: envelopeFrom,
			smtpAddr:     host + ":" + port,
			smtpAuth:     auth,
		}, nil
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("email: load AWS config: %w", err)
	}

	return &Client{
		ses:          sesv2.NewFromConfig(cfg),
		from:         from,
		envelopeFrom: envelopeFrom,
	}, nil
}

// Message holds the fields for a single transactional email.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string // plain-text fallback
}

// Send delivers a single email. If the Client is nil (EMAIL_FROM unset), it is a no-op.
func (c *Client) Send(ctx context.Context, m Message) error {
	if c == nil {
		return nil
	}
	if c.smtpAddr != "" {
		msg, err := buildSMTPMessage(c.from, m)
		if err != nil {
			return err
		}
		return smtp.SendMail(c.smtpAddr, c.smtpAuth, c.envelopeFrom, []string{m.To}, msg)
	}

	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(c.from),
		Destination: &types.Destination{
			ToAddresses: []string{m.To},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(m.Subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &types.Body{
					Html: &types.Content{
						Data:    aws.String(m.HTML),
						Charset: aws.String("UTF-8"),
					},
					Text: &types.Content{
						Data:    aws.String(m.Text),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	}

	_, err := c.ses.SendEmail(ctx, input)
	return err
}

func parseEnvelopeFrom(from string) (string, error) {
	addr, err := mail.ParseAddress(from)
	if err == nil {
		return addr.Address, nil
	}
	if strings.Contains(from, "@") && !strings.ContainsAny(from, "<>") {
		return from, nil
	}
	return "", err
}

func buildSMTPMessage(from string, m Message) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	headers := map[string]string{
		"From":         from,
		"To":           m.To,
		"Subject":      mime.QEncoding.Encode("utf-8", m.Subject),
		"MIME-Version": "1.0",
		"Content-Type": fmt.Sprintf(`multipart/alternative; boundary="%s"`, writer.Boundary()),
	}
	for k, v := range headers {
		if _, err := fmt.Fprintf(&buf, "%s: %s\r\n", k, v); err != nil {
			return nil, err
		}
	}
	if _, err := buf.WriteString("\r\n"); err != nil {
		return nil, err
	}

	if err := writePart(writer, "text/plain", m.Text); err != nil {
		return nil, err
	}
	if err := writePart(writer, "text/html", m.HTML); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func writePart(writer *multipart.Writer, contentType, body string) error {
	part, err := writer.CreatePart(map[string][]string{
		"Content-Type":              {contentType + `; charset="UTF-8"`},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}

	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(body)); err != nil {
		_ = qp.Close()
		return err
	}
	return qp.Close()
}
