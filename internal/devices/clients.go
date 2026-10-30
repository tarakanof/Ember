package devices

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
	MaxClients = 64
)

var (
	errTooManyClients = fmt.Errorf("%w: at most %d client tokens; delete one first", ErrBody, MaxClients)
)

const (
	ClientsKey        = "clients_json"
	KindClient        = "client"
	ClientIDPrefix    = "client-"
	ClientTokenPrefix = "ekc_"
)

type ClientRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	TokenSHA256 string    `json:"token_sha256"`
	Scopes      []string  `json:"scopes"`
	Sources     []string  `json:"sources,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (c ClientRecord) clone() ClientRecord {
	c.Scopes = slices.Clone(c.Scopes)
	c.Sources = slices.Clone(c.Sources)
	return c
}

type ClientView struct {
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

func (c ClientRecord) view() ClientView {
	c = c.clone()
	return ClientView{
		ID:        c.ID,
		Kind:      KindClient,
		Name:      c.Name,
		CreatedAt: c.CreatedAt.UTC().Truncate(time.Second),
		Scopes:    c.Scopes,
		Sources:   c.Sources,
	}
}

type clientState struct {
	Clients []ClientRecord `json:"clients"`
}

type ClientCreds struct {
	ID      string
	Scopes  []string
	Sources []string
}

type ClientRegistry struct {
	kv  func() KV
	now func() time.Time

	mu      sync.Mutex
	clients []ClientRecord
	loadErr error
}

func NewClientRegistry(kv func() KV) *ClientRegistry {
	return &ClientRegistry{kv: kv, now: time.Now}
}

func (r *ClientRegistry) Load() error {
	kv := r.kv()
	if kv == nil {
		return nil
	}
	clients, err := ReadClients(kv)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadErr = err
	if err == nil {
		r.clients = clients
	}
	return err
}

func (r *ClientRegistry) Fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients, r.loadErr = nil, err
}

func (r *ClientRegistry) LoadError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadErr
}

func ReadClients(kv KV) ([]ClientRecord, error) {
	blob, ok, err := kv.GetSetting(ClientsKey)
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

func WriteClients(kv KV, clients []ClientRecord) error {
	if clients == nil {
		clients = []ClientRecord{}
	}
	blob, err := json.Marshal(clientState{Clients: clients})
	if err != nil {
		return fmt.Errorf("encode client tokens: %w", err)
	}
	if err := kv.PutSetting(ClientsKey, string(blob)); err != nil {
		return fmt.Errorf("persist client tokens: %w", err)
	}
	return nil
}

func (r *ClientRegistry) mutate(fn func([]ClientRecord) ([]ClientRecord, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadErr != nil {
		return fmt.Errorf("client token store load failed, writes refused until restart: %w", r.loadErr)
	}
	next := make([]ClientRecord, 0, len(r.clients)+1)
	for _, c := range r.clients {
		next = append(next, c.clone())
	}
	next, err := fn(next)
	if err != nil {
		return err
	}
	if kv := r.kv(); kv != nil {
		if err := WriteClients(kv, next); err != nil {
			return err
		}
	}
	r.clients = next
	return nil
}

func (r *ClientRegistry) List() []ClientView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ClientView, 0, len(r.clients))
	for _, c := range r.clients {
		out = append(out, c.view())
	}
	return out
}

func (r *ClientRegistry) Exists(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return indexClient(r.clients, id) >= 0
}

func indexClient(clients []ClientRecord, id string) int {
	return slices.IndexFunc(clients, func(c ClientRecord) bool { return c.ID == id })
}

func sortClients(clients []ClientRecord) {
	slices.SortFunc(clients, func(a, b ClientRecord) int { return strings.Compare(a.ID, b.ID) })
}

func (r *ClientRegistry) Mint(name string, scopes, sources []string) (ClientView, string, error) {
	token := NewToken(ClientTokenPrefix)
	var view ClientView
	err := r.mutate(func(cs []ClientRecord) ([]ClientRecord, error) {
		if len(cs) >= MaxClients {
			return nil, errTooManyClients
		}
		id := clientID()
		for indexClient(cs, id) >= 0 {
			id = clientID()
		}
		c := ClientRecord{
			ID:          id,
			Name:        name,
			TokenSHA256: TokenHash(token),
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

func (r *ClientRegistry) Rename(id, name string) (ClientView, error) {
	var view ClientView
	err := r.mutate(func(cs []ClientRecord) ([]ClientRecord, error) {
		i := indexClient(cs, id)
		if i < 0 {
			return nil, ErrNotFound
		}
		cs[i].Name = name
		view = cs[i].view()
		return cs, nil
	})
	return view, err
}

func (r *ClientRegistry) Rotate(id string) (ClientView, string, error) {
	token := NewToken(ClientTokenPrefix)
	var view ClientView
	err := r.mutate(func(cs []ClientRecord) ([]ClientRecord, error) {
		i := indexClient(cs, id)
		if i < 0 {
			return nil, ErrNotFound
		}
		cs[i].TokenSHA256 = TokenHash(token)
		view = cs[i].view()
		return cs, nil
	})
	if err != nil {
		return ClientView{}, "", err
	}
	return view, token, nil
}

func (r *ClientRegistry) Remove(id string) error {
	return r.mutate(func(cs []ClientRecord) ([]ClientRecord, error) {
		i := indexClient(cs, id)
		if i < 0 {
			return nil, ErrNotFound
		}
		return slices.Delete(cs, i, i+1), nil
	})
}

func (r *ClientRegistry) Authenticate(token string) (ClientCreds, bool, error) {
	if !strings.HasPrefix(token, ClientTokenPrefix) {
		return ClientCreds{}, false, nil
	}
	h := []byte(TokenHash(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadErr != nil {
		return ClientCreds{}, false, fmt.Errorf("client token store load failed: %w", r.loadErr)
	}
	match := -1
	for i, c := range r.clients {
		if subtle.ConstantTimeCompare(h, []byte(c.TokenSHA256)) == 1 {
			match = i
		}
	}
	if match < 0 {
		return ClientCreds{}, false, nil
	}
	c := r.clients[match]
	return ClientCreds{ID: c.ID, Scopes: slices.Clone(c.Scopes), Sources: slices.Clone(c.Sources)}, true, nil
}

func clientID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return ClientIDPrefix + hex.EncodeToString(b)
}

func MigrateClientRecords(kv KV) (int, error) {
	if kv == nil {
		return 0, nil
	}
	blob, ok, err := kv.GetSetting(StateKey)
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
	var moved []ClientRecord
	kept := make([]json.RawMessage, 0, len(records))
	for _, raw := range records {
		var head struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return 0, fmt.Errorf("decode device record: %w", err)
		}
		if head.Kind != KindClient {
			kept = append(kept, raw)
			continue
		}
		var c ClientRecord
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
	clients, err := ReadClients(kv)
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
	if err := WriteClients(kv, clients); err != nil {
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
	if err := kv.PutSetting(StateKey, string(out)); err != nil {
		return 0, fmt.Errorf("persist devices: %w", err)
	}
	return len(moved), nil
}
