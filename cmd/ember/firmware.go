package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	firmwareMinBytes       = 64 << 10
	firmwareMaxBytes       = 4 << 20
	firmwareELFMaxBytes    = 64 << 20
	firmwareKept           = 5
	firmwareChipESP32S3    = 9
	firmwareProject        = "cinder"
	firmwareDevSeedMarker  = "CINDER-DEV-SEED-BUILD"
	firmwareChannelRelease = "release"
	firmwareChannelTest    = "test"
	firmwareBinName        = "cinder.bin"
	firmwareELFName        = "cinder.elf"
	firmwareMetaName       = "meta.json"
	firmwareTempPrefix     = ".tmp-"
	firmwareAsidePrefix    = ".old-"
)

var (
	errFirmwareBadImage     = errors.New("bad_image")
	errFirmwareWrongChip    = errors.New("wrong_chip")
	errFirmwareWrongProject = errors.New("wrong_project")
	errFirmwareBadVersion   = errors.New("bad_version")
	errFirmwareDevSeed      = errors.New("dev_seed_build")
	errFirmwareELFMismatch  = errors.New("elf_mismatch")
	errFirmwareNotFound     = errors.New("firmware version not found")
	errFirmwareConflict     = errors.New("this version is stored with other bytes; upload with ?replace=1")
	errFirmwareInUse        = errors.New("this version is a device's update target")
	errFirmwareOff          = errors.New("firmware storage unavailable")
)

var (
	semverPattern        = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
	firmwareBuildPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)
)

type firmwareDesc struct {
	Version   string
	Project   string
	IDFVer    string
	ELFSHA256 string
	Build     string
}

func parseFirmwareImage(b []byte) (firmwareDesc, error) {
	if len(b) < firmwareMinBytes || len(b) > firmwareMaxBytes || b[0] != 0xE9 {
		return firmwareDesc{}, errFirmwareBadImage
	}
	if binary.LittleEndian.Uint16(b[12:14]) != firmwareChipESP32S3 {
		return firmwareDesc{}, errFirmwareWrongChip
	}
	if binary.LittleEndian.Uint32(b[32:36]) != 0xABCD5432 {
		return firmwareDesc{}, errFirmwareBadImage
	}
	d := firmwareDesc{
		Version:   cString(b[48:80]),
		Project:   cString(b[80:112]),
		IDFVer:    cString(b[144:176]),
		ELFSHA256: hex.EncodeToString(b[176:208]),
		Build:     hex.EncodeToString(b[176:180]),
	}
	if d.Project != firmwareProject {
		return firmwareDesc{}, errFirmwareWrongProject
	}
	if !semverPattern.MatchString(d.Version) {
		return firmwareDesc{}, errFirmwareBadVersion
	}
	return d, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func compareSemver(a, b string) int {
	coreA, preA, _ := strings.Cut(a, "-")
	coreB, preB, _ := strings.Cut(b, "-")
	pa, pb := strings.Split(coreA, "."), strings.Split(coreB, ".")
	for i := range min(len(pa), len(pb)) {
		if c := compareNumeric(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	ia, ib := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := range min(len(ia), len(ib)) {
		na, errA := strconv.ParseUint(ia[i], 10, 64)
		nb, errB := strconv.ParseUint(ib[i], 10, 64)
		var c int
		switch {
		case errA == nil && errB == nil:
			c = cmp.Compare(na, nb)
		case errA == nil:
			c = -1
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(ia[i], ib[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(ia), len(ib))
}

func compareNumeric(a, b string) int {
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

type seedScanner struct {
	needles [][]byte
	keep    int
	tail    []byte
	found   bool
}

func newSeedScanner(token string) *seedScanner {
	s := &seedScanner{needles: [][]byte{[]byte(firmwareDevSeedMarker)}}
	if token != "" {
		s.needles = append(s.needles, []byte(token))
	}
	for _, n := range s.needles {
		s.keep = max(s.keep, len(n)-1)
	}
	return s
}

func (s *seedScanner) Write(p []byte) (int, error) {
	if s.found {
		return len(p), nil
	}
	buf := append(s.tail, p...)
	for _, n := range s.needles {
		if bytes.Contains(buf, n) {
			s.found = true
			s.tail = nil
			return len(p), nil
		}
	}
	s.tail = append(s.tail[:0], buf[max(0, len(buf)-s.keep):]...)
	return len(p), nil
}

func devSeedFound(b []byte, token string) bool {
	s := newSeedScanner(token)
	_, _ = s.Write(b)
	return s.found
}

type firmwareImage struct {
	Build      string    `json:"build"`
	Channel    string    `json:"channel"`
	ELF        bool      `json:"elf"`
	IDFVer     string    `json:"idf_ver"`
	Project    string    `json:"project"`
	SHA256     string    `json:"sha256"`
	Size       int       `json:"size"`
	UploadedAt time.Time `json:"uploaded_at"`
	Version    string    `json:"version"`
}

type firmwareMeta struct {
	firmwareImage
	ELFSHA256 string `json:"elf_sha256"`
}

type firmwareStore struct {
	dir string
	now func() time.Time

	mu        sync.Mutex
	index     map[string]firmwareMeta
	writeFile func(path string, data []byte) error
}

func newFirmwareStore(dir string) *firmwareStore {
	return &firmwareStore{dir: dir, now: time.Now, index: map[string]firmwareMeta{}, writeFile: writeFileAtomic}
}

func validChannel(ch string) bool {
	return ch == firmwareChannelRelease || ch == firmwareChannelTest
}

func (s *firmwareStore) versionDir(version string) string {
	return filepath.Join(s.dir, version)
}

func (s *firmwareStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create firmware dir: %w", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read firmware dir: %w", err)
	}
	s.index = map[string]firmwareMeta{}
	var errs []error
	entries, errs = s.recoverAsideLocked(entries)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, ok, err := s.loadVersionLocked(e.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			errs = append(errs, removeAllErr(s.versionDir(e.Name())))
			continue
		}
		s.index[m.Version] = m
	}
	return errors.Join(errs...)
}

func (s *firmwareStore) recoverAsideLocked(entries []os.DirEntry) ([]os.DirEntry, []error) {
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	var errs []error
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), firmwareAsidePrefix)
		if !ok || !e.IsDir() {
			continue
		}
		version := rest
		if i := strings.LastIndex(rest, "-"); i > 0 {
			version = rest[:i]
		}
		if semverPattern.MatchString(version) && !present[version] {
			if err := os.Rename(filepath.Join(s.dir, e.Name()), s.versionDir(version)); err != nil {
				errs = append(errs, fmt.Errorf("restore firmware version: %w", err))
				continue
			}
			present[version] = true
			continue
		}
		errs = append(errs, removeAllErr(filepath.Join(s.dir, e.Name())))
	}
	fresh, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, append(errs, fmt.Errorf("read firmware dir: %w", err))
	}
	return fresh, errs
}

func (s *firmwareStore) loadVersionLocked(name string) (firmwareMeta, bool, error) {
	dir := s.versionDir(name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return firmwareMeta{}, false, fmt.Errorf("read firmware version dir: %w", err)
	}
	present := map[string]bool{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".tmp-") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return firmwareMeta{}, false, fmt.Errorf("remove leftover firmware file: %w", err)
			}
			continue
		}
		present[e.Name()] = true
	}
	if !present[firmwareMetaName] || !present[firmwareBinName] || !semverPattern.MatchString(name) {
		return firmwareMeta{}, false, nil
	}
	blob, err := os.ReadFile(filepath.Join(dir, firmwareMetaName))
	if err != nil {
		return firmwareMeta{}, false, fmt.Errorf("read firmware meta: %w", err)
	}
	var m firmwareMeta
	if err := json.Unmarshal(blob, &m); err != nil || m.Version != name {
		return firmwareMeta{}, false, nil
	}
	if m.ELF != present[firmwareELFName] {
		if present[firmwareELFName] {
			if err := os.Remove(filepath.Join(dir, firmwareELFName)); err != nil {
				return firmwareMeta{}, false, fmt.Errorf("remove unrecorded firmware elf: %w", err)
			}
		}
		m.ELF = false
		if err := s.writeMetaLocked(m); err != nil {
			return firmwareMeta{}, false, err
		}
	}
	return m, true, nil
}

func removeAllErr(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove firmware version: %w", err)
	}
	return nil
}

func (s *firmwareStore) writeMetaLocked(m firmwareMeta) error {
	return s.writeMetaIn(s.versionDir(m.Version), m)
}

func (s *firmwareStore) writeMetaIn(dir string, m firmwareMeta) error {
	blob, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode firmware meta: %w", err)
	}
	return s.writeFile(filepath.Join(dir, firmwareMetaName), blob)
}

func (s *firmwareStore) put(d firmwareDesc, body []byte, channel string, replace bool, inUse, keep func(string) bool) (firmwareImage, bool, error) {
	sum := sha256.Sum256(body)
	m := firmwareMeta{
		firmwareImage: firmwareImage{
			Build:   d.Build,
			Channel: channel,
			IDFVer:  d.IDFVer,
			Project: d.Project,
			SHA256:  hex.EncodeToString(sum[:]),
			Size:    len(body),
			Version: d.Version,
		},
		ELFSHA256: d.ELFSHA256,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.index[d.Version]
	if exists {
		if old.SHA256 == m.SHA256 {
			return old.firmwareImage, false, nil
		}
		if !replace {
			return firmwareImage{}, false, errFirmwareConflict
		}
		if inUse != nil && inUse(d.Version) {
			return firmwareImage{}, false, errFirmwareInUse
		}
	}
	m.UploadedAt = s.now().UTC().Truncate(time.Second)
	tmp, err := os.MkdirTemp(s.dir, firmwareTempPrefix+d.Version+"-*")
	if err != nil {
		return firmwareImage{}, false, fmt.Errorf("create firmware temp dir: %w", err)
	}
	if err := s.writeFile(filepath.Join(tmp, firmwareBinName), body); err != nil {
		_ = os.RemoveAll(tmp)
		return firmwareImage{}, false, err
	}
	if err := s.writeMetaIn(tmp, m); err != nil {
		_ = os.RemoveAll(tmp)
		return firmwareImage{}, false, err
	}
	if err := s.swapInLocked(tmp, d.Version, exists); err != nil {
		_ = os.RemoveAll(tmp)
		return firmwareImage{}, false, err
	}
	s.index[d.Version] = m
	return m.firmwareImage, true, s.pruneLocked(d.Version, keep)
}

func (s *firmwareStore) swapInLocked(tmp, version string, exists bool) error {
	dir := s.versionDir(version)
	if !exists {
		_ = os.RemoveAll(dir)
		if err := os.Rename(tmp, dir); err != nil {
			return fmt.Errorf("move firmware version in place: %w", err)
		}
		return nil
	}
	aside, err := os.MkdirTemp(s.dir, firmwareAsidePrefix+version+"-*")
	if err != nil {
		return fmt.Errorf("create firmware aside dir: %w", err)
	}
	if err := os.Remove(aside); err != nil {
		return fmt.Errorf("reserve firmware aside name: %w", err)
	}
	if err := os.Rename(dir, aside); err != nil {
		return fmt.Errorf("move old firmware version aside: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.Rename(aside, dir)
		return fmt.Errorf("move firmware version in place: %w", err)
	}
	return removeAllErr(aside)
}

func (s *firmwareStore) pruneLocked(just string, keep func(string) bool) error {
	versions := s.versionsLocked()
	var errs []error
	for i, v := range versions {
		if i < firmwareKept || v == just || (keep != nil && keep(v)) {
			continue
		}
		delete(s.index, v)
		errs = append(errs, removeAllErr(s.versionDir(v)))
	}
	return errors.Join(errs...)
}

func (s *firmwareStore) versionsLocked() []string {
	out := make([]string, 0, len(s.index))
	for v := range s.index {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b string) int { return compareSemver(b, a) })
	return out
}

func (s *firmwareStore) putELF(version string, body io.Reader, token string) error {
	s.mu.Lock()
	m, ok := s.index[version]
	s.mu.Unlock()
	if !ok {
		return errFirmwareNotFound
	}
	path := filepath.Join(s.versionDir(version), firmwareELFName)
	tmp, err := createTempFor(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	scan := newSeedScanner(token)
	if _, err := io.Copy(io.MultiWriter(tmp, hash, scan), body); err != nil {
		discardTemp(tmp)
		return fmt.Errorf("write firmware elf: %w", err)
	}
	if scan.found {
		discardTemp(tmp)
		return errFirmwareDevSeed
	}
	if hex.EncodeToString(hash.Sum(nil)) != m.ELFSHA256 {
		discardTemp(tmp)
		return errFirmwareELFMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.index[version]
	if !ok || cur.SHA256 != m.SHA256 {
		discardTemp(tmp)
		return errFirmwareNotFound
	}
	if err := commitTemp(tmp, path); err != nil {
		return err
	}
	cur.ELF = true
	if err := s.writeMetaLocked(cur); err != nil {
		return err
	}
	s.index[version] = cur
	return nil
}

func (s *firmwareStore) get(version string) (firmwareMeta, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.index[version]
	return m, ok
}

func (s *firmwareStore) byBuild(build string) (firmwareMeta, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.versionsLocked() {
		if m := s.index[v]; m.Build == build && m.ELF {
			return m, true
		}
	}
	return firmwareMeta{}, false
}

func (s *firmwareStore) list() []firmwareImage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]firmwareImage, 0, len(s.index))
	for _, v := range s.versionsLocked() {
		out = append(out, s.index[v].firmwareImage)
	}
	return out
}

func (s *firmwareStore) newestAbove(running string, accept func(firmwareMeta) bool) (firmwareMeta, bool) {
	if !semverPattern.MatchString(running) {
		return firmwareMeta{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.versionsLocked() {
		if compareSemver(v, running) <= 0 {
			break
		}
		if m := s.index[v]; accept == nil || accept(m) {
			return m, true
		}
	}
	return firmwareMeta{}, false
}

func (s *firmwareStore) setChannel(version, channel string) (firmwareImage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.index[version]
	if !ok {
		return firmwareImage{}, errFirmwareNotFound
	}
	if m.Channel == channel {
		return m.firmwareImage, nil
	}
	m.Channel = channel
	if err := s.writeMetaLocked(m); err != nil {
		return firmwareImage{}, err
	}
	s.index[version] = m
	return m.firmwareImage, nil
}

func (s *firmwareStore) remove(version string, inUse func(string) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.index[version]; !ok {
		return errFirmwareNotFound
	}
	if inUse != nil && inUse(version) {
		return errFirmwareInUse
	}
	delete(s.index, version)
	return removeAllErr(s.versionDir(version))
}

func (s *firmwareStore) open(version, name string) (firmwareMeta, *os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.index[version]
	if !ok || (name == firmwareELFName && !m.ELF) {
		return firmwareMeta{}, nil, errFirmwareNotFound
	}
	f, err := os.Open(filepath.Join(s.versionDir(version), name))
	if err != nil {
		return firmwareMeta{}, nil, fmt.Errorf("open firmware file: %w", err)
	}
	return m, f, nil
}

func (s *firmwareStore) usage() (int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	err := filepath.WalkDir(s.dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return len(s.index), total, err
}
