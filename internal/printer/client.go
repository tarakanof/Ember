package printer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	Timeout       = 5 * time.Second
	MaxThumbBytes = 1 << 20
	maxJSONBytes  = 64 << 10
)

var (
	ErrWrongPassword = errors.New("prusalink: wrong password")
	ErrNoThumbnail   = errors.New("prusalink: no thumbnail")
	ErrBadThumbnail  = errors.New("prusalink: unusable thumbnail")
	errStaleTwice    = errors.New("prusalink: nonce stale twice")
	pngMagic         = []byte("\x89PNG\r\n\x1a\n")
)

type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("prusalink: HTTP %d", e.Code) }

type Client struct {
	base  string
	host  string
	creds Credentials
	http  *http.Client
	chal  challenge
}

func NewClient(base string, creds Credentials, hc *http.Client) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("prusalink: bad base URL")
	}
	if hc == nil {
		hc = &http.Client{}
	}
	c := *hc
	if c.Timeout == 0 {
		c.Timeout = Timeout
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimRight(base, "/"), host: u.Host, creds: creds, http: &c}, nil
}

func (c *Client) BaseURL() string { return c.base }

func (c *Client) do(ctx context.Context, path string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("prusalink: bad path")
	}
	sentAuth := false
	staleRetry := false
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return nil, fmt.Errorf("prusalink: bad path")
		}
		if req.URL.Host != c.host {
			return nil, fmt.Errorf("prusalink: path leaves the printer")
		}
		sentAuth = c.chal.nonce != ""
		if sentAuth {
			req.Header.Set("Authorization", authorization(c.creds.User, c.creds.Password, c.chal, http.MethodGet, req.URL.RequestURI()))
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, scrub(err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			return resp, nil
		}
		ch, ok := parseChallenge(resp.Header.Get("WWW-Authenticate"))
		drain(resp)
		if !ok {
			return nil, &StatusError{Code: http.StatusUnauthorized}
		}
		switch {
		case !sentAuth:
			c.chal = ch
		case ch.stale && !staleRetry:
			staleRetry = true
			c.chal = ch
		case ch.stale:
			return nil, errStaleTwice
		default:
			c.chal = challenge{}
			return nil, ErrWrongPassword
		}
	}
	return nil, errStaleTwice
}

func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("prusalink: %s: %w", ue.Op, ue.Err)
	}
	return fmt.Errorf("prusalink: %w", err)
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxJSONBytes))
	_ = resp.Body.Close()
}

func (c *Client) getJSON(ctx context.Context, path string, dst any) (bool, error) {
	resp, err := c.do(ctx, path)
	if err != nil {
		return false, err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNoContent:
		return false, nil
	default:
		return false, &StatusError{Code: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(dst); err != nil {
		return false, fmt.Errorf("prusalink: decode %s: %w", path, err)
	}
	return true, nil
}

type StatusDoc struct {
	Job     *StatusJob    `json:"job"`
	Printer StatusPrinter `json:"printer"`
}

type StatusPrinter struct {
	State        string  `json:"state"`
	TempBed      float64 `json:"temp_bed"`
	TargetBed    float64 `json:"target_bed"`
	TempNozzle   float64 `json:"temp_nozzle"`
	TargetNozzle float64 `json:"target_nozzle"`
}

type StatusJob struct {
	ID               int      `json:"id"`
	Progress         float64  `json:"progress"`
	TimeRemaining    *float64 `json:"time_remaining"`
	TimePrinting     *float64 `json:"time_printing"`
	FilamentChangeIn *float64 `json:"filament_change_in"`
}

type JobDoc struct {
	ID    int    `json:"id"`
	State string `json:"state"`
	File  struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Refs        struct {
			Thumbnail string `json:"thumbnail"`
		} `json:"refs"`
	} `json:"file"`
}

type VersionDoc struct {
	Firmware string `json:"firmware"`
	Hostname string `json:"hostname"`
}

func (c *Client) Status(ctx context.Context) (StatusDoc, error) {
	var d StatusDoc
	ok, err := c.getJSON(ctx, "/api/v1/status", &d)
	if err == nil && !ok {
		err = fmt.Errorf("prusalink: empty status")
	}
	return d, err
}

func (c *Client) Job(ctx context.Context) (*JobDoc, error) {
	var d JobDoc
	ok, err := c.getJSON(ctx, "/api/v1/job", &d)
	if err != nil || !ok {
		return nil, err
	}
	return &d, nil
}

func (c *Client) Version(ctx context.Context) (VersionDoc, error) {
	var d VersionDoc
	_, err := c.getJSON(ctx, "/api/version", &d)
	return d, err
}

func (c *Client) Thumbnail(ctx context.Context, ref string) ([]byte, error) {
	if !strings.HasPrefix(ref, "/thumb/") {
		return nil, ErrNoThumbnail
	}
	resp, err := c.do(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusNoContent:
		return nil, ErrNoThumbnail
	default:
		return nil, &StatusError{Code: resp.StatusCode}
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "image/png" {
		return nil, fmt.Errorf("%w: type %q", ErrBadThumbnail, mt)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxThumbBytes+1))
	if err != nil {
		return nil, scrub(err)
	}
	if len(b) > MaxThumbBytes {
		return nil, fmt.Errorf("%w: over %d bytes", ErrBadThumbnail, MaxThumbBytes)
	}
	if !bytes.HasPrefix(b, pngMagic) {
		return nil, fmt.Errorf("%w: not a PNG", ErrBadThumbnail)
	}
	if err := CheckThumb(b); err != nil {
		return nil, err
	}
	return b, nil
}
