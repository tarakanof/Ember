package printer

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	URLAuto            = "auto"
	DefaultName        = "CORE One"
	DefaultDoneHoldMin = 30
	DefaultUser        = "maker"

	PasswordEnv = "EMBER_PRUSALINK_PASSWORD"
	UserEnv     = "EMBER_PRUSALINK_USER"

	maxNameRunes   = 64
	maxDoneHoldMin = 24 * 60
)

type Config struct {
	Enabled         bool   `json:"enabled"`
	URL             string `json:"url"`
	Name            string `json:"name"`
	ClockApp        *bool  `json:"clock_app"`
	NotifyDone      *bool  `json:"notify_done"`
	NotifyAttention *bool  `json:"notify_attention"`
	DoneHoldMin     int    `json:"done_hold_min"`
}

func (c Config) Auto() bool { return c.URL == "" || c.URL == URLAuto }

func (c Config) ClockAppEnabled() bool        { return c.ClockApp == nil || *c.ClockApp }
func (c Config) NotifyDoneEnabled() bool      { return c.NotifyDone == nil || *c.NotifyDone }
func (c Config) NotifyAttentionEnabled() bool { return c.NotifyAttention == nil || *c.NotifyAttention }

func (c *Config) FillAbsent() {
	on := func() *bool { b := true; return &b }
	if c.URL == "" {
		c.URL = URLAuto
	}
	if c.Name == "" {
		c.Name = DefaultName
	}
	if c.ClockApp == nil {
		c.ClockApp = on()
	}
	if c.NotifyDone == nil {
		c.NotifyDone = on()
	}
	if c.NotifyAttention == nil {
		c.NotifyAttention = on()
	}
}

func (c *Config) Normalize() {
	c.URL = strings.TrimRight(strings.TrimSpace(c.URL), "/")
	c.FillAbsent()
}

func (c *Config) ApplyDefaults() {
	c.Normalize()
	if c.DoneHoldMin == 0 {
		c.DoneHoldMin = DefaultDoneHoldMin
	}
}

func (c Config) Validate() error {
	if !c.Auto() {
		if err := ValidURL(c.URL); err != nil {
			return fmt.Errorf("printer.url: %w", err)
		}
	}
	if utf8.RuneCountInString(c.Name) > maxNameRunes {
		return fmt.Errorf("printer.name: at most %d characters", maxNameRunes)
	}
	if c.DoneHoldMin < 0 || c.DoneHoldMin > maxDoneHoldMin {
		return fmt.Errorf("printer.done_hold_min %d out of range [1, %d] (0 = %d)", c.DoneHoldMin, maxDoneHoldMin, DefaultDoneHoldMin)
	}
	return nil
}

func ValidURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New(`want "auto" or http://host`)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.Hostname() == "" {
		return errors.New(`want "auto" or http://host`)
	}
	if u.User != nil {
		return errors.New("no credentials in the URL; the password comes from " + PasswordEnv)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("only scheme, host and port")
	}
	return nil
}

type Credentials struct {
	User     string
	Password string
}

func CredentialsFromEnv(getenv func(string) string) Credentials {
	c := Credentials{User: strings.TrimSpace(getenv(UserEnv)), Password: getenv(PasswordEnv)}
	if c.User == "" {
		c.User = DefaultUser
	}
	return c
}

func (c Credentials) LogValue() slog.Value {
	return slog.GroupValue(slog.String("user", c.User), slog.Bool("password_set", c.Password != ""))
}

func (c Credentials) String() string {
	return fmt.Sprintf("user=%s password_set=%t", c.User, c.Password != "")
}

func (c Credentials) GoString() string { return c.String() }
