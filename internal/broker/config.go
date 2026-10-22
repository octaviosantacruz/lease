package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/octaviosantacruz/lease/internal/store"
)

type Entitlement struct {
	ID         string   `json:"id"`
	Provider   string   `json:"provider"`
	Target     string   `json:"target"`
	Role       string   `json:"role"`
	MaxTTL     string   `json:"max_ttl"`
	Requesters []string `json:"requesters"`
	Approvers  []string `json:"approvers"`
}

type Config struct {
	Entitlements []Entitlement   `json:"entitlements"`
	Postgres     *PostgresConfig `json:"postgres,omitempty"`
}

type PostgresConfig struct {
	Target     string            `json:"target"`
	Namespace  string            `json:"namespace"`
	DSNEnv     string            `json:"dsn_env"`
	Principals map[string]string `json:"principals"`
	Roles      map[string]string `json:"roles"`
}

func Load(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("config: trailing JSON")
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	seen := map[string]bool{}
	if len(c.Entitlements) == 0 {
		return errors.New("config: at least one entitlement required")
	}
	for _, e := range c.Entitlements {
		ttl, err := time.ParseDuration(e.MaxTTL)
		if err != nil || ttl < time.Second || ttl > 24*time.Hour || e.ID == "" || e.Target == "" || e.Role == "" || seen[e.ID] || len(e.Requesters) == 0 || len(e.Approvers) == 0 {
			return errors.New("config: invalid entitlement")
		}
		seen[e.ID] = true
		for _, p := range append(append([]string{}, e.Requesters...), e.Approvers...) {
			if p == "" || p == "system:reconciler" {
				return errors.New("config: invalid principal")
			}
		}
		switch e.Provider {
		case "lab":
			if len(e.Target) < 5 || e.Target[:4] != "lab/" {
				return errors.New("config: lab target must start with lab/")
			}
		case "postgres":
			if c.Postgres == nil || c.Postgres.Target != e.Target || c.Postgres.Roles[e.Role] == "" {
				return errors.New("config: unmapped PostgreSQL entitlement")
			}
			for _, p := range e.Requesters {
				if c.Postgres.Principals[p] == "" {
					return fmt.Errorf("config: no PostgreSQL mapping for %s", p)
				}
			}
		default:
			return errors.New("config: unsupported provider")
		}
	}
	return nil
}
func (e Entitlement) Record() store.Entitlement {
	ttl, _ := time.ParseDuration(e.MaxTTL)
	return store.Entitlement{ID: e.ID, Provider: e.Provider, Target: e.Target, Role: e.Role, MaxTTL: ttl}
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (c Config) Find(id string) (Entitlement, bool) {
	for _, e := range c.Entitlements {
		if e.ID == id {
			return e, true
		}
	}
	return Entitlement{}, false
}
