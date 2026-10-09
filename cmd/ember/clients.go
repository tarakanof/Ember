package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	clientsKey        = "clients_json"
	clientKind        = "client"
	clientIDPrefix    = "client-"
	clientTokenPrefix = "ekc_"
)

type clientRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	TokenSHA256 string    `json:"token_sha256"`
	Scopes      []string  `json:"scopes"`
	Sources     []string  `json:"sources,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (c clientRecord) clone() clientRecord {
	c.Scopes = slices.Clone(c.Scopes)
	c.Sources = slices.Clone(c.Sources)
	return c
}

type clientView struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	HwID            string     `json:"hw_id"`
	Name            string     `json:"name"`
	CreatedAt       time.Time  `json:"created_at"`
	ConfigVersion   int        `json:"config_version"`
	RotationPending bool       `json:"rotation_pending"`
	RotatedAt       *time.Time `json:"rotated_at"`
	LastCheckin     *struct{}  `json:"last_checkin"`
	Scopes          []string   `json:"scopes,omitempty"`
	Sources         []string   `json:"sources,omitempty"`
}

func (c clientRecord) view() clientView {
	c = c.clone()
	return clientView{
		ID:        c.ID,
		Kind:      clientKind,
		Name:      c.Name,
		CreatedAt: c.CreatedAt.UTC().Truncate(time.Second),
		Scopes:    c.Scopes,
		Sources:   c.Sources,
	}
}

type clientState struct {
	Clients []clientRecord `json:"clients"`
}

type clientCreds struct {
	id      string
	scopes  []string
	sources []string
}

type clientRegistry struct {
	kv  func() settingsKV
	now func() time.Time

	mu      sync.Mutex
	clients []clientRecord
	loadErr error
}

func newClientRegistry(kv func() settingsKV) *clientRegistry {
	return &clientRegistry{kv: kv, now: time.Now}
}

func (r *clientRegistry) load() error {
	kv := r.kv()
	if kv == nil {
		return nil
	}
	clients, err := readClients(kv)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadErr = err
	if err == nil {
		r.clients = clients
	}
	return err
}

func (r *clientRegistry) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients, r.loadErr = nil, err
}

func (r *clientRegistry) loadError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadErr
}

func readClients(kv settingsKV) ([]clientRecord, error) {
	blob, ok, err := kv.GetSetting(clientsKey)
	if err != nil {
		return nil, fmt.Errorf("read client tokens: %w", err)
	}
	if !ok {
		return nil, nil
	}
	var st clientState
	if err := json.Unmarshal([]byte(blob), &st); err != nil {
		return nil, fmt.Errorf("decode client tokens: %w", err)
	}
	return st.Clients, nil
}

func writeClients(kv settingsKV, clients []clientRecord) error {
	if clients == nil {
		clients = []clientRecord{}
	}
	blob, err := json.Marshal(clientState{Clients: clients})
	if err != nil {
		return fmt.Errorf("encode client tokens: %w", err)
	}
	if err := kv.PutSetting(clientsKey, string(blob)); err != nil {
		return fmt.Errorf("persist client tokens: %w", err)
	}
	return nil
}

func (r *clientRegistry) mutate(fn func([]clientRecord) ([]clientRecord, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadErr != nil {
		return fmt.Errorf("client token store load failed, writes refused until restart: %w", r.loadErr)
	}
	next := make([]clientRecord, 0, len(r.clients)+1)
	for _, c := range r.clients {
		next = append(next, c.clone())
	}
	next, err := fn(next)
	if err != nil {
		return err
	}
	if kv := r.kv(); kv != nil {
		if err := writeClients(kv, next); err != nil {
			return err
		}
	}
	r.clients = next
	return nil
}

func (r *clientRegistry) list() []clientView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]clientView, 0, len(r.clients))
	for _, c := range r.clients {
		out = append(out, c.view())
	}
	return out
}

func (r *clientRegistry) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.clients, func(c clientRecord) bool { return c.ID == id })
}

func indexClient(clients []clientRecord, id string) int {
	return slices.IndexFunc(clients, func(c clientRecord) bool { return c.ID == id })
}

func sortClients(clients []clientRecord) {
	slices.SortFunc(clients, func(a, b clientRecord) int { return strings.Compare(a.ID, b.ID) })
}

func (r *clientRegistry) mint(name string, scopes, sources []string) (clientView, string, error) {
	token := newToken(clientTokenPrefix)
	var view clientView
	err := r.mutate(func(cs []clientRecord) ([]clientRecord, error) {
		if len(cs) >= maxClients {
			return nil, errTooManyClients
		}
		id := clientID()
		for indexClient(cs, id) >= 0 {
			id = clientID()
		}
		c := clientRecord{
			ID:          id,
			Name:        name,
			TokenSHA256: tokenHash(token),
			Scopes:      scopes,
			Sources:     sources,
			CreatedAt:   r.now().UTC(),
		}
		cs = append(cs, c)
		sortClients(cs)
		view = c.view()
		return cs, nil
	})
	return view, token, err
}

func (r *clientRegistry) rename(id, name string) (clientView, error) {
	var view clientView
	err := r.mutate(func(cs []clientRecord) ([]clientRecord, error) {
		i := indexClient(cs, id)
		if i < 0 {
			return nil, errDeviceNotFound
		}
		cs[i].Name = name
		view = cs[i].view()
		return cs, nil
	})
	return view, err
}

func (r *clientRegistry) rotate(id string) (clientView, string, error) {
	token := newToken(clientTokenPrefix)
	var view clientView
	err := r.mutate(func(cs []clientRecord) ([]clientRecord, error) {
		i := indexClient(cs, id)
		if i < 0 {
			return nil, errDeviceNotFound
		}
		cs[i].TokenSHA256 = tokenHash(token)
		view = cs[i].view()
		return cs, nil
	})
	if err != nil {
		return clientView{}, "", err
	}
	return view, token, nil
}

func (r *clientRegistry) remove(id string) error {
	return r.mutate(func(cs []clientRecord) ([]clientRecord, error) {
		i := indexClient(cs, id)
		if i < 0 {
			return nil, errDeviceNotFound
		}
		return slices.Delete(cs, i, i+1), nil
	})
}

func (r *clientRegistry) authenticate(token string) (clientCreds, bool, error) {
	if !strings.HasPrefix(token, clientTokenPrefix) {
		return clientCreds{}, false, nil
	}
	h := []byte(tokenHash(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadErr != nil {
		return clientCreds{}, false, fmt.Errorf("client token store load failed: %w", r.loadErr)
	}
	match := -1
	for i, c := range r.clients {
		if subtle.ConstantTimeCompare(h, []byte(c.TokenSHA256)) == 1 {
			match = i
		}
	}
	if match < 0 {
		return clientCreds{}, false, nil
	}
	c := r.clients[match]
	return clientCreds{id: c.ID, scopes: slices.Clone(c.Scopes), sources: slices.Clone(c.Sources)}, true, nil
}

func clientID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return clientIDPrefix + hex.EncodeToString(b)
}

func migrateClientRecords(kv settingsKV) (int, error) {
	if kv == nil {
		return 0, nil
	}
	blob, ok, err := kv.GetSetting(devicesKey)
	if err != nil {
		return 0, fmt.Errorf("read devices: %w", err)
	}
	if !ok {
		return 0, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blob), &top); err != nil {
		return 0, fmt.Errorf("decode devices: %w", err)
	}
	var records []json.RawMessage
	if raw, ok := top["devices"]; ok {
		if err := json.Unmarshal(raw, &records); err != nil {
			return 0, fmt.Errorf("decode devices: %w", err)
		}
	}
	var moved []clientRecord
	kept := make([]json.RawMessage, 0, len(records))
	for _, raw := range records {
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return 0, fmt.Errorf("decode device record: %w", err)
		}
		if head.Kind != clientKind {
			kept = append(kept, raw)
			continue
		}
		var c clientRecord
		if err := json.Unmarshal(raw, &c); err != nil {
			return 0, fmt.Errorf("decode client record: %w", err)
		}
		if c.ID == "" || c.TokenSHA256 == "" {
			return 0, errors.New("client record without id or token hash")
		}
		moved = append(moved, c)
	}
	if len(moved) == 0 {
		return 0, nil
	}
	clients, err := readClients(kv)
	if err != nil {
		return 0, err
	}
	for _, c := range moved {
		if i := indexClient(clients, c.ID); i >= 0 {
			clients[i] = c
		} else {
			clients = append(clients, c)
		}
	}
	sortClients(clients)
	if err := writeClients(kv, clients); err != nil {
		return 0, err
	}
	devices, err := json.Marshal(kept)
	if err != nil {
		return 0, fmt.Errorf("encode devices: %w", err)
	}
	top["devices"] = devices
	out, err := json.Marshal(top)
	if err != nil {
		return 0, fmt.Errorf("encode devices: %w", err)
	}
	if err := kv.PutSetting(devicesKey, string(out)); err != nil {
		return 0, fmt.Errorf("persist devices: %w", err)
	}
	return len(moved), nil
}

func (a *App) loadRegistries() error {
	moved, err := migrateClientRecords(a.devices.kv())
	if err != nil {
		err = fmt.Errorf("move client tokens out of the device registry: %w", err)
		a.devices.fail(err)
		a.clients.fail(err)
		return err
	}
	if moved > 0 {
		a.logger.Info("client tokens moved out of the device registry", "count", moved)
	}
	return errors.Join(a.devices.load(), a.clients.load())
}
