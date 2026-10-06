package main

import (
	"cmp"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const coredumpsKept = 3

var (
	errCoredumpNotFound = errors.New("core dump not found")
	errCoredumpBusy     = errors.New("an upload for this device is already running")
	errCoredumpGone     = errors.New("device deleted during upload")
)

type coredumpMeta struct {
	ID         string    `json:"id"`
	Size       int       `json:"size"`
	FW         string    `json:"fw"`
	ReceivedAt time.Time `json:"received_at"`
	Reason     string    `json:"reason"`
	Task       string    `json:"task"`
	PC         string    `json:"pc"`
}

type coredumpSidecar struct {
	coredumpMeta
	Seq uint64 `json:"seq"`
}

type coredumpStore struct {
	dir string
	now func() time.Time

	mu       sync.Mutex
	inFlight map[string]bool
}

func newCoredumpStore(dir string) *coredumpStore {
	return &coredumpStore{dir: dir, now: time.Now, inFlight: map[string]bool{}}
}

func coredumpID(body []byte) (string, error) {
	if len(body) < 8 {
		return "", errors.New("core dump must be at least 8 bytes")
	}
	payload, trailer := body[:len(body)-4], body[len(body)-4:]
	sum := crc32.ChecksumIEEE(payload)
	if binary.LittleEndian.Uint32(trailer) != sum {
		return "", errors.New("core dump checksum trailer does not match its bytes")
	}
	return fmt.Sprintf("%08x", sum), nil
}

func (s *coredumpStore) begin(device string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[device] {
		return false
	}
	s.inFlight[device] = true
	return true
}

func (s *coredumpStore) end(device string) {
	s.mu.Lock()
	delete(s.inFlight, device)
	s.mu.Unlock()
}

func (s *coredumpStore) uploading(device string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight[device]
}

func (s *coredumpStore) deviceDir(device string) string {
	return filepath.Join(s.dir, device)
}

func (s *coredumpStore) has(device, id string) bool {
	if !coredumpIDPattern.MatchString(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.deviceDir(device), id+".json"))
	return err == nil
}

func (s *coredumpStore) put(device string, meta coredumpMeta, body []byte, exists func() bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !exists() {
		return errCoredumpGone
	}
	dir := s.deviceDir(device)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create core dump dir: %w", err)
	}
	if err := sweepCoredumpDir(dir); err != nil {
		return err
	}
	stored, err := s.storedLocked(device)
	if err != nil {
		return err
	}
	meta.ReceivedAt = s.now().UTC().Truncate(time.Second)
	next := coredumpSidecar{coredumpMeta: meta, Seq: 1}
	if len(stored) > 0 {
		next.Seq = stored[0].Seq + 1
	}
	blob, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode core dump meta: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, meta.ID+".bin"), body); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, meta.ID+".json"), blob); err != nil {
		_ = os.Remove(filepath.Join(dir, meta.ID+".bin"))
		return err
	}
	kept := 1
	for _, old := range stored {
		if old.ID == meta.ID {
			continue
		}
		if kept < coredumpsKept {
			kept++
			continue
		}
		if err := s.removeLocked(device, old.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *coredumpStore) sweep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read core dump dir: %w", err)
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			errs = append(errs, sweepCoredumpDir(filepath.Join(s.dir, e.Name())))
		}
	}
	return errors.Join(errs...)
}

func sweepCoredumpDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read core dump dir: %w", err)
	}
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		stale := strings.HasPrefix(name, ".") && strings.Contains(name, ".tmp-")
		if id, ok := strings.CutSuffix(name, ".bin"); ok && coredumpIDPattern.MatchString(id) {
			stale = !present[id+".json"]
		}
		if id, ok := strings.CutSuffix(name, ".json"); ok && coredumpIDPattern.MatchString(id) {
			stale = !present[id+".bin"]
		}
		if !stale {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove leftover core dump file: %w", err))
		}
	}
	return errors.Join(errs...)
}

func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp core dump file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write core dump file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync core dump file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close core dump file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename core dump file: %w", err)
	}
	return nil
}

func (s *coredumpStore) list(device string) ([]coredumpMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked(device)
}

func (s *coredumpStore) listLocked(device string) ([]coredumpMeta, error) {
	stored, err := s.storedLocked(device)
	if err != nil {
		return nil, err
	}
	out := make([]coredumpMeta, 0, len(stored))
	for _, d := range stored {
		out = append(out, d.coredumpMeta)
	}
	return out, nil
}

func (s *coredumpStore) storedLocked(device string) ([]coredumpSidecar, error) {
	entries, err := os.ReadDir(s.deviceDir(device))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read core dump dir: %w", err)
	}
	var out []coredumpSidecar
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !coredumpIDPattern.MatchString(id) {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(s.deviceDir(device), e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read core dump meta: %w", err)
		}
		var m coredumpSidecar
		if err := json.Unmarshal(blob, &m); err != nil || m.ID != id {
			continue
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b coredumpSidecar) int {
		if c := cmp.Compare(b.Seq, a.Seq); c != 0 {
			return c
		}
		return b.ReceivedAt.Compare(a.ReceivedAt)
	})
	return out, nil
}

func (s *coredumpStore) open(device, id string) (coredumpMeta, []byte, error) {
	if !coredumpIDPattern.MatchString(id) {
		return coredumpMeta{}, nil, errCoredumpNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, err := os.ReadFile(filepath.Join(s.deviceDir(device), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return coredumpMeta{}, nil, errCoredumpNotFound
	}
	if err != nil {
		return coredumpMeta{}, nil, fmt.Errorf("read core dump meta: %w", err)
	}
	var m coredumpMeta
	if err := json.Unmarshal(blob, &m); err != nil {
		return coredumpMeta{}, nil, fmt.Errorf("decode core dump meta: %w", err)
	}
	body, err := os.ReadFile(filepath.Join(s.deviceDir(device), id+".bin"))
	if errors.Is(err, os.ErrNotExist) {
		return coredumpMeta{}, nil, errCoredumpNotFound
	}
	if err != nil {
		return coredumpMeta{}, nil, fmt.Errorf("read core dump: %w", err)
	}
	return m, body, nil
}

func (s *coredumpStore) remove(device, id string) error {
	if !coredumpIDPattern.MatchString(id) {
		return errCoredumpNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(filepath.Join(s.deviceDir(device), id+".json")); errors.Is(err, os.ErrNotExist) {
		return errCoredumpNotFound
	}
	return s.removeLocked(device, id)
}

func (s *coredumpStore) removeLocked(device, id string) error {
	dir := s.deviceDir(device)
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove core dump meta: %w", err)
	}
	if err := os.Remove(filepath.Join(dir, id+".bin")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove core dump: %w", err)
	}
	return nil
}

func (s *coredumpStore) removeDevice(device string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.deviceDir(device)); err != nil {
		return fmt.Errorf("remove core dumps: %w", err)
	}
	return nil
}
