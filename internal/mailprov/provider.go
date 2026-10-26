// Package mailprov manages external mailboxes. It never returns credentials.
package mailprov

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/CamiloCaceres/lolad/internal/core"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrManual = fmt.Errorf("%w: manual provider; manage this mailbox at your provider", core.ErrInvalid)
var ErrExists = fmt.Errorf("%w: mailbox already exists; attach it manually", core.ErrExists)

type Provider interface {
	Create(context.Context, string, string) error
	List(context.Context) ([]string, error)
	Delete(context.Context, string) error
	Reset(context.Context, string, string) error
	Domains(context.Context) ([]string, error)
}
type Manual struct{}

func (Manual) Create(context.Context, string, string) error { return ErrManual }
func (Manual) List(context.Context) ([]string, error)       { return []string{}, nil }
func (Manual) Delete(context.Context, string) error         { return ErrManual }
func (Manual) Reset(context.Context, string, string) error  { return ErrManual }
func (Manual) Domains(context.Context) ([]string, error)    { return []string{}, nil }

// Purelymail uses the documented v0 JSON API. Response bodies and transport
// errors are deliberately never included in errors: either may contain secrets.
type Purelymail struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func (p *Purelymail) call(ctx context.Context, method string, body any, out any) error {
	base := p.BaseURL
	if base == "" {
		base = "https://purelymail.com/api/v0"
	}
	b, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/"+method, bytes.NewReader(b))
	if e != nil {
		return errors.New("invalid provider endpoint")
	}
	req.Header.Set("Purelymail-Api-Token", p.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	if p.Client != nil {
		*client = *p.Client
		if client.Timeout == 0 {
			client.Timeout = 20 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := client.Do(req)
	if e != nil {
		return errors.New("mail provider unavailable; check connectivity and retry")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("mail provider rejected %s (HTTP %d)", method, res.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&envelope) != nil {
		return errors.New("invalid mail provider response")
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return errors.New("mail provider rejected request; check token, domain and mailbox")
	}
	if len(envelope.Result) == 0 || !strings.HasPrefix(strings.TrimSpace(string(envelope.Result)), "{") {
		return errors.New("mail provider response missing result")
	}
	if out != nil && json.Unmarshal(envelope.Result, out) != nil {
		return errors.New("invalid mail provider result")
	}
	return nil
}
func (p *Purelymail) Create(ctx context.Context, address, password string) error {
	local, domain, ok := strings.Cut(address, "@")
	if !ok {
		return errors.New("invalid mailbox address")
	}
	return p.call(ctx, "createUser", map[string]any{"userName": local, "domainName": domain, "password": password, "sendWelcomeEmail": false, "enablePasswordReset": false}, nil)
}
func (p *Purelymail) List(ctx context.Context) ([]string, error) {
	var r struct {
		Users []string `json:"users"`
	}
	e := p.call(ctx, "listUser", struct{}{}, &r)
	if e == nil && r.Users == nil {
		e = errors.New("invalid mail provider user listing")
	}
	return r.Users, e
}
func (p *Purelymail) Delete(ctx context.Context, address string) error {
	return p.call(ctx, "deleteUser", map[string]string{"userName": address}, nil)
}
func (p *Purelymail) Reset(ctx context.Context, address, password string) error {
	return p.call(ctx, "modifyUser", map[string]string{"userName": address, "newPassword": password}, nil)
}
func (p *Purelymail) Domains(ctx context.Context) ([]string, error) {
	var r struct {
		Domains []struct {
			Name string `json:"name"`
		} `json:"domains"`
	}
	e := p.call(ctx, "listDomains", struct{}{}, &r)
	var out []string
	for _, d := range r.Domains {
		out = append(out, d.Name)
	}
	return out, e
}
