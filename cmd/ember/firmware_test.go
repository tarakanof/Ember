package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fwOpts struct {
	version string
	project string
	idf     string
	chip    uint16
	size    int
	elf     []byte
	seed    byte
}

func fakeELF(seed byte) []byte {
	b := make([]byte, 4096)
	copy(b, "\x7fELF")
	for i := 4; i < len(b); i++ {
		b[i] = seed + byte(i*13)
	}
	return b
}

func fakeFirmware(o fwOpts) []byte {
	if o.project == "" {
		o.project = "cinder"
	}
	if o.idf == "" {
		o.idf = "v5.5.5"
	}
	if o.chip == 0 {
		o.chip = 9
	}
	if o.size == 0 {
		o.size = 70 << 10
	}
	if o.elf == nil {
		o.elf = fakeELF(o.seed)
	}
	b := make([]byte, o.size)
	for i := 288; i < len(b); i++ {
		b[i] = o.seed + byte(i*7)
	}
	b[0] = 0xE9
	binary.LittleEndian.PutUint16(b[12:], o.chip)
	binary.LittleEndian.PutUint32(b[32:], 0xABCD5432)
	copy(b[48:80], o.version)
	copy(b[80:112], o.project)
	copy(b[144:176], o.idf)
	sum := sha256.Sum256(o.elf)
	copy(b[176:208], sum[:])
	return b
}

func TestParseFirmwareImageReadsTheAppDescription(t *testing.T) {
	elf := fakeELF(3)
	img := fakeFirmware(fwOpts{version: "0.9.14", elf: elf})
	d, err := parseFirmwareImage(img)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(elf)
	if d.Version != "0.9.14" || d.Project != "cinder" || d.IDFVer != "v5.5.5" {
		t.Fatalf("desc = %+v", d)
	}
	if d.ELFSHA256 != hex.EncodeToString(sum[:]) || d.Build != hex.EncodeToString(sum[:4]) {
		t.Fatalf("elf sha = %s build = %s", d.ELFSHA256, d.Build)
	}
}

func TestParseFirmwareImageRejects(t *testing.T) {
	good := func() []byte { return fakeFirmware(fwOpts{version: "0.9.14"}) }
	cases := []struct {
		name string
		img  []byte
		want error
	}{
		{"too small", fakeFirmware(fwOpts{version: "0.9.14", size: 64<<10 - 1}), errFirmwareBadImage},
		{"too large", fakeFirmware(fwOpts{version: "0.9.14", size: 4<<20 + 1}), errFirmwareBadImage},
		{"bad magic", func() []byte { b := good(); b[0] = 0xE8; return b }(), errFirmwareBadImage},
		{"bad desc magic", func() []byte { b := good(); b[32] = 0; return b }(), errFirmwareBadImage},
		{"esp32 chip", fakeFirmware(fwOpts{version: "0.9.14", chip: 2}), errFirmwareWrongChip},
		{"other project", fakeFirmware(fwOpts{version: "0.9.14", project: "blink"}), errFirmwareWrongProject},
		{"no version", fakeFirmware(fwOpts{}), errFirmwareBadVersion},
		{"two parts", fakeFirmware(fwOpts{version: "0.9"}), errFirmwareBadVersion},
		{"leading zero", fakeFirmware(fwOpts{version: "0.09.1"}), errFirmwareBadVersion},
		{"v prefix", fakeFirmware(fwOpts{version: "v0.9.14"}), errFirmwareBadVersion},
		{"build metadata", fakeFirmware(fwOpts{version: "0.9.14+abc"}), errFirmwareBadVersion},
		{"git describe dirty", fakeFirmware(fwOpts{version: "0.9.14-dirty/x"}), errFirmwareBadVersion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseFirmwareImage(c.img); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestParseFirmwareImageAcceptsAPrerelease(t *testing.T) {
	d, err := parseFirmwareImage(fakeFirmware(fwOpts{version: "0.9.14-3-g1a2b3c4"}))
	if err != nil || d.Version != "0.9.14-3-g1a2b3c4" {
		t.Fatalf("desc = %+v, err = %v", d, err)
	}
}

func TestDevSeedScanFindsTheTokenAndTheMarker(t *testing.T) {
	const token = "secret-master-token"
	for name, needle := range map[string]string{"token": token, "marker": "CINDER-DEV-SEED-BUILD"} {
		t.Run(name, func(t *testing.T) {
			img := fakeFirmware(fwOpts{version: "0.9.14"})
			copy(img[40000:], needle)
			if !devSeedFound(img, token) {
				t.Fatal("not found")
			}
		})
	}
	if devSeedFound(fakeFirmware(fwOpts{version: "0.9.14"}), token) {
		t.Fatal("clean image flagged")
	}
	if devSeedFound(fakeFirmware(fwOpts{version: "0.9.14"}), "") {
		t.Fatal("empty token matched")
	}
}

func TestDevSeedScannerFindsANeedleSplitAcrossWrites(t *testing.T) {
	const token = "secret-master-token"
	body := bytes.Repeat([]byte{0x55}, 100000)
	copy(body[65530:], token)
	s := newSeedScanner(token)
	for chunk := range slices.Chunk(body, 32768) {
		_, _ = s.Write(chunk)
	}
	if !s.found {
		t.Fatal("split token not found")
	}
}

func TestCompareSemver(t *testing.T) {
	ordered := []string{"0.9.2", "0.9.13", "0.9.14-1", "0.9.14-alpha", "0.9.14-alpha.1", "0.9.14-beta", "0.9.14", "0.10.0", "1.0.0"}
	for i := range ordered {
		for j := range ordered {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := compareSemver(ordered[i], ordered[j]); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func newFWStore(t *testing.T) *firmwareStore {
	t.Helper()
	s := newFirmwareStore(filepath.Join(t.TempDir(), "firmware", "cinder-knob"))
	s.now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	if err := s.load(); err != nil {
		t.Fatal(err)
	}
	return s
}

func putFW(t *testing.T, s *firmwareStore, img []byte, channel string) (firmwareImage, bool) {
	t.Helper()
	d, err := parseFirmwareImage(img)
	if err != nil {
		t.Fatal(err)
	}
	got, created, err := s.put(d, img, channel, false, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got, created
}

func TestFirmwareStorePutIsIdempotentAndPinsTheSHA(t *testing.T) {
	s := newFWStore(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	got, created := putFW(t, s, img, "test")
	sum := sha256.Sum256(img)
	if !created || got.SHA256 != hex.EncodeToString(sum[:]) || got.Size != len(img) || got.Channel != "test" || got.ELF {
		t.Fatalf("image = %+v created = %v", got, created)
	}
	if _, created := putFW(t, s, img, "test"); created {
		t.Fatal("same bytes created again")
	}
	other := fakeFirmware(fwOpts{version: "0.9.14", seed: 9})
	d, _ := parseFirmwareImage(other)
	if _, _, err := s.put(d, other, "test", false, nil, nil, nil); !errors.Is(err, errFirmwareConflict) {
		t.Fatalf("other bytes err = %v, want conflict", err)
	}
	if _, _, err := s.put(d, other, "test", true, func(string) bool { return true }, nil, nil); !errors.Is(err, errFirmwareInUse) {
		t.Fatalf("replace of a target err = %v, want in use", err)
	}
	replaced, created, err := s.put(d, other, "test", true, func(string) bool { return false }, nil, nil)
	if err != nil || !created || replaced.SHA256 == got.SHA256 {
		t.Fatalf("replace = %+v %v %v", replaced, created, err)
	}
}

func TestFirmwareStoreSurvivesARestart(t *testing.T) {
	s := newFWStore(t)
	img := fakeFirmware(fwOpts{version: "0.9.14"})
	want, _ := putFW(t, s, img, "release")
	again := newFirmwareStore(s.dir)
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	list := again.list()
	if len(list) != 1 || list[0] != want {
		t.Fatalf("list = %+v, want %+v", list, want)
	}
}

func TestFirmwareStoreSweepsHalfWrittenVersions(t *testing.T) {
	s := newFWStore(t)
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	half := filepath.Join(s.dir, "0.9.15")
	if err := os.MkdirAll(half, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(half, "cinder.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(s.dir, "0.9.14", ".cinder.elf.tmp-123")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	again := newFirmwareStore(s.dir)
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(half); !os.IsNotExist(err) {
		t.Fatalf("half-written version kept: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp file kept: %v", err)
	}
	if len(again.list()) != 1 {
		t.Fatalf("list = %+v", again.list())
	}
}

func TestFirmwareStoreListsNewestVersionFirst(t *testing.T) {
	s := newFWStore(t)
	for i, v := range []string{"0.9.9", "0.10.0", "0.9.14-1", "0.9.14"} {
		putFW(t, s, fakeFirmware(fwOpts{version: v, seed: byte(i)}), "test")
	}
	var got []string
	for _, img := range s.list() {
		got = append(got, img.Version)
	}
	if want := []string{"0.10.0", "0.9.14", "0.9.14-1", "0.9.9"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestFirmwareStorePruneKeepsFiveAndProtectedVersions(t *testing.T) {
	s := newFWStore(t)
	versions := []string{"0.9.1", "0.9.2", "0.9.3", "0.9.4", "0.9.5", "0.9.6", "0.9.7"}
	for i, v := range versions {
		d, _ := parseFirmwareImage(fakeFirmware(fwOpts{version: v, seed: byte(i)}))
		img := fakeFirmware(fwOpts{version: v, seed: byte(i)})
		if _, _, err := s.put(d, img, "test", false, nil, func(v string) bool { return v == "0.9.1" }, nil); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, img := range s.list() {
		got = append(got, img.Version)
	}
	if want := []string{"0.9.7", "0.9.6", "0.9.5", "0.9.4", "0.9.3", "0.9.1"}; !slices.Equal(got, want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "0.9.2")); !os.IsNotExist(err) {
		t.Fatalf("pruned dir kept: %v", err)
	}
}

func TestFirmwareStoreNeverPrunesTheImageJustWritten(t *testing.T) {
	s := newFWStore(t)
	for i, v := range []string{"0.9.10", "0.9.11", "0.9.12", "0.9.13", "0.9.14"} {
		putFW(t, s, fakeFirmware(fwOpts{version: v, seed: byte(i)}), "test")
	}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.1", seed: 40}), "test")
	if _, ok := s.get("0.9.1"); !ok {
		t.Fatal("the upload was pruned")
	}
	if len(s.list()) != 6 {
		t.Fatalf("list = %+v, want the five newest plus the upload", s.list())
	}
}

func TestFirmwareStoreELFMustMatchTheImage(t *testing.T) {
	s := newFWStore(t)
	elf := fakeELF(5)
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14", elf: elf}), "test")
	if err := s.putELF("0.9.14", bytes.NewReader(fakeELF(6)), ""); !errors.Is(err, errFirmwareELFMismatch) {
		t.Fatalf("other elf err = %v", err)
	}
	if err := s.putELF("0.9.15", bytes.NewReader(elf), ""); !errors.Is(err, errFirmwareNotFound) {
		t.Fatalf("unknown version err = %v", err)
	}
	if err := s.putELF("0.9.14", bytes.NewReader(elf), ""); err != nil {
		t.Fatal(err)
	}
	m, ok := s.get("0.9.14")
	if !ok || !m.ELF {
		t.Fatalf("meta = %+v", m)
	}
	byBuild, ok := s.byBuild(m.Build)
	if !ok || byBuild.Version != "0.9.14" {
		t.Fatalf("by build = %+v %v", byBuild, ok)
	}
	f, err := os.ReadFile(filepath.Join(s.dir, "0.9.14", "cinder.elf"))
	if err != nil || !bytes.Equal(f, elf) {
		t.Fatalf("stored elf differs: %v", err)
	}
}

func TestFirmwareStoreELFDevSeedIsRefused(t *testing.T) {
	s := newFWStore(t)
	elf := fakeELF(5)
	copy(elf[1000:], "CINDER-DEV-SEED-BUILD")
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14", elf: elf}), "test")
	if err := s.putELF("0.9.14", bytes.NewReader(elf), "tok"); !errors.Is(err, errFirmwareDevSeed) {
		t.Fatalf("err = %v, want dev seed", err)
	}
	if m, _ := s.get("0.9.14"); m.ELF {
		t.Fatal("dev-seed elf stored")
	}
}

func TestFirmwareStoreRemoveAndChannel(t *testing.T) {
	s := newFWStore(t)
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	img, err := s.setChannel("0.9.14", "release")
	if err != nil || img.Channel != "release" {
		t.Fatalf("channel = %+v %v", img, err)
	}
	if err := s.remove("0.9.14", func(string) bool { return true }, nil); !errors.Is(err, errFirmwareInUse) {
		t.Fatalf("remove target err = %v", err)
	}
	if err := s.remove("0.9.14", func(string) bool { return false }, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.remove("0.9.14", func(string) bool { return false }, nil); !errors.Is(err, errFirmwareNotFound) {
		t.Fatalf("second remove err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "0.9.14")); !os.IsNotExist(err) {
		t.Fatalf("dir kept: %v", err)
	}
}

func TestFirmwareStoreReplaceKeepsTheOldVersionWhenTheWriteFails(t *testing.T) {
	s := newFWStore(t)
	old, _ := putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	other := fakeFirmware(fwOpts{version: "0.9.14", seed: 9})
	d, _ := parseFirmwareImage(other)
	s.writeFile = func(path string, data []byte) error {
		if filepath.Base(path) == firmwareBinName {
			return errors.New("disk full")
		}
		return writeFileAtomic(path, data)
	}
	if _, _, err := s.put(d, other, "test", true, func(string) bool { return false }, nil, nil); err == nil {
		t.Fatal("put succeeded")
	}
	m, ok := s.get("0.9.14")
	if !ok || m.SHA256 != old.SHA256 {
		t.Fatalf("old version lost: %+v %v", m, ok)
	}
	got, err := os.ReadFile(filepath.Join(s.dir, "0.9.14", firmwareBinName))
	if err != nil || sha256.Sum256(got) != sha256.Sum256(fakeFirmware(fwOpts{version: "0.9.14"})) {
		t.Fatalf("old bin on disk changed: %v", err)
	}
	again := newFirmwareStore(s.dir)
	if err := again.load(); err != nil || len(again.list()) != 1 || again.list()[0].SHA256 != old.SHA256 {
		t.Fatalf("after reload: %+v %v", again.list(), err)
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestFirmwareStoreRestoresAnOldVersionMovedAsideByACrash(t *testing.T) {
	s := newFWStore(t)
	old, _ := putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14-rc1"}), "test")
	if err := os.Rename(filepath.Join(s.dir, "0.9.14-rc1"), filepath.Join(s.dir, ".old-0.9.14-rc1-123")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, ".tmp-0.9.14-rc1-456"), 0o700); err != nil {
		t.Fatal(err)
	}
	again := newFirmwareStore(s.dir)
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	if m, ok := again.get("0.9.14-rc1"); !ok || m.SHA256 != old.SHA256 {
		t.Fatalf("not restored: %+v %v", m, ok)
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 1 || entries[0].Name() != "0.9.14-rc1" {
		t.Fatalf("entries = %v", entries)
	}
}

type unblockRecorder struct {
	t     *testing.T
	s     *firmwareStore
	calls [][]string
}

func (u *unblockRecorder) unblock(versions []string) {
	if u.s.mu.TryLock() {
		u.s.mu.Unlock()
		u.t.Error("unblock ran without the store lock held")
	}
	u.calls = append(u.calls, slices.Clone(versions))
}

func (u *unblockRecorder) take() [][]string {
	out := u.calls
	u.calls = nil
	return out
}

func putFWUnblock(t *testing.T, s *firmwareStore, img []byte, replace bool, u *unblockRecorder) error {
	t.Helper()
	d, err := parseFirmwareImage(img)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.put(d, img, "test", replace, nil, nil, u.unblock)
	return err
}

func TestFirmwareStoreUnblocksOnlyRemovedOrReplacedVersionsUnderItsLock(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	if err := putFWUnblock(t, s, fakeFirmware(fwOpts{version: "0.9.1"}), false, u); err != nil {
		t.Fatal(err)
	}
	if err := putFWUnblock(t, s, fakeFirmware(fwOpts{version: "0.9.1"}), true, u); err != nil {
		t.Fatal(err)
	}
	if got := u.take(); got != nil {
		t.Fatalf("new or same bytes unblocked %v", got)
	}
	if err := putFWUnblock(t, s, fakeFirmware(fwOpts{version: "0.9.1", seed: 7}), true, u); err != nil {
		t.Fatal(err)
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.1"}}, slices.Equal) {
		t.Fatalf("replace unblocked %v, want [[0.9.1]]", got)
	}
	for i := 2; i <= firmwareKept+1; i++ {
		if err := putFWUnblock(t, s, fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", i)}), false, u); err != nil {
			t.Fatal(err)
		}
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.1"}}, slices.Equal) {
		t.Fatalf("eviction unblocked %v, want [[0.9.1]]", got)
	}
	if err := s.remove("0.9.2", nil, u.unblock); err != nil {
		t.Fatal(err)
	}
	if err := s.remove("0.9.2", nil, u.unblock); !errors.Is(err, errFirmwareNotFound) {
		t.Fatalf("second remove = %v", err)
	}
	if err := s.remove("0.9.3", func(string) bool { return true }, u.unblock); !errors.Is(err, errFirmwareInUse) {
		t.Fatalf("in-use remove = %v", err)
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.2"}, {"0.9.2"}}, slices.Equal) {
		t.Fatalf("remove unblocked %v, want [[0.9.2] [0.9.2]]", got)
	}
}

func failRemoveOf(s *firmwareStore, version string) {
	s.removeAll = func(path string) error {
		if filepath.Base(path) == version {
			return errors.New("injected failure")
		}
		return os.RemoveAll(path)
	}
}

func TestFirmwareStoreKeepsTheBlockWhenRemoveAllFails(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	failRemoveOf(s, "0.9.14")
	if err := s.remove("0.9.14", nil, u.unblock); err == nil {
		t.Fatal("remove succeeded")
	}
	if got := u.take(); got != nil {
		t.Fatalf("failed remove unblocked %v", got)
	}
	if _, ok := s.get("0.9.14"); !ok || len(s.list()) != 1 {
		t.Fatalf("failed remove dropped the entry: %+v", s.list())
	}
	s.removeAll = os.RemoveAll
	if err := s.remove("0.9.14", nil, u.unblock); err != nil {
		t.Fatalf("retry = %v", err)
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.14"}}, slices.Equal) {
		t.Fatalf("retry unblocked %v, want [[0.9.14]]", got)
	}
}

func TestFirmwareStoreKeepsTheBlockWhenAnEvictionFails(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.1"}), "test")
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.2"}), "test")
	failRemoveOf(s, "0.9.1")
	for i := 3; i <= firmwareKept+2; i++ {
		img := fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", i)})
		d, _ := parseFirmwareImage(img)
		_, created, err := s.put(d, img, "test", false, nil, nil, u.unblock)
		if !created || (err != nil) != (i >= firmwareKept+1) {
			t.Fatalf("put 0.9.%d = %v %v, want created with an error once 0.9.1 is due for eviction", i, created, err)
		}
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.2"}}, slices.Equal) {
		t.Fatalf("evictions unblocked %v, want [[0.9.2]]", got)
	}
	if _, ok := s.get("0.9.1"); !ok {
		t.Fatal("failed eviction dropped the entry")
	}
	s.removeAll = os.RemoveAll
	if err := putFWUnblock(t, s, fakeFirmware(fwOpts{version: "0.9.8"}), false, u); err != nil {
		t.Fatal(err)
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.3", "0.9.1"}}, slices.Equal) {
		t.Fatalf("retried eviction unblocked %v, want 0.9.3 and 0.9.1", got)
	}
	if _, ok := s.get("0.9.1"); ok {
		t.Fatal("retried eviction kept 0.9.1")
	}
}

func failOnBase(prefix string, real func(string) error) func(string) error {
	return func(path string) error {
		if strings.HasPrefix(filepath.Base(path), prefix) {
			return errors.New("injected failure")
		}
		return real(path)
	}
}

func binSHA(t *testing.T, s *firmwareStore, version string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.versionDir(version), firmwareBinName))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func reloadFW(t *testing.T, s *firmwareStore) *firmwareStore {
	t.Helper()
	again := newFirmwareStore(s.dir)
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	return again
}

func replaceWithStuckAside(t *testing.T, s *firmwareStore, version string, u *unblockRecorder) firmwareImage {
	t.Helper()
	other := fakeFirmware(fwOpts{version: version, seed: 9})
	d, _ := parseFirmwareImage(other)
	s.removeAll = failOnBase(firmwareAsidePrefix, os.RemoveAll)
	img, created, err := s.put(d, other, "test", true, nil, nil, u.unblock)
	if err == nil || !created {
		t.Fatalf("put = %v %v, want created with a cleanup error", created, err)
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{version}}, slices.Equal) {
		t.Fatalf("replace unblocked %v, want [[%s]]", got, version)
	}
	return img
}

func TestFirmwareStoreDeleteOfAnUnindexedVersionPurgesItsFilesThenUnblocks(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	if err := os.MkdirAll(s.versionDir("0.9.14"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, ".old-0.9.15-123"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, ".old-0.9.16-123"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.removeAll = failOnBase(".old-0.9.16", os.RemoveAll)
	if err := s.remove("0.9.16", nil, u.unblock); err == nil || errors.Is(err, errFirmwareNotFound) {
		t.Fatalf("remove with a stuck copy = %v, want a storage error", err)
	}
	if got := u.take(); got != nil {
		t.Fatalf("stuck copy unblocked %v", got)
	}
	for _, v := range []string{"0.9.14", "0.9.15"} {
		if err := s.remove(v, nil, u.unblock); !errors.Is(err, errFirmwareNotFound) {
			t.Fatalf("remove %s = %v, want not found", v, err)
		}
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.14"}, {"0.9.15"}}, slices.Equal) {
		t.Fatalf("purged 404s unblocked %v, want [[0.9.14] [0.9.15]]", got)
	}
	if names := dirNames(t, s.dir); names != ".old-0.9.16-123" {
		t.Fatalf("entries = %v", names)
	}
}

func TestFirmwareStoreDeleteKeepsTheBlockWhenTheDirCannotBeRead(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	s.readDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("injected failure") }
	if err := s.remove("0.9.14", nil, u.unblock); err == nil || errors.Is(err, errFirmwareNotFound) {
		t.Fatalf("remove = %v, want a storage error", err)
	}
	if got := u.take(); got != nil {
		t.Fatalf("unreadable dir unblocked %v", got)
	}
}

func TestFirmwareStoreReplaceCommitsWhenTheOldCopyCannotBeRemoved(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	img := replaceWithStuckAside(t, s, "0.9.14", u)
	if m, ok := s.get("0.9.14"); !ok || m.SHA256 != img.SHA256 || binSHA(t, s, "0.9.14") != img.SHA256 {
		t.Fatalf("index %+v %v disagrees with disk or upload %s", m, ok, img.SHA256)
	}
	again := reloadFW(t, s)
	if m, ok := again.get("0.9.14"); !ok || m.SHA256 != img.SHA256 {
		t.Fatalf("after reload: %+v %v", m, ok)
	}
	if names := dirNames(t, s.dir); names != "0.9.14" {
		t.Fatalf("entries after reload = %v", names)
	}
}

func TestFirmwareStoreFailedDeleteNeverLeavesOnlyThePreReplaceCopy(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	img := replaceWithStuckAside(t, s, "0.9.14", u)
	if err := s.remove("0.9.14", nil, u.unblock); err == nil {
		t.Fatal("remove succeeded with an old copy stuck")
	}
	if got := u.take(); got != nil {
		t.Fatalf("failed remove unblocked %v", got)
	}
	again := reloadFW(t, s)
	if m, ok := again.get("0.9.14"); !ok || m.SHA256 != img.SHA256 {
		t.Fatalf("after restart 0.9.14 = %+v %v, want the replacement %s", m, ok, img.SHA256)
	}
	u2 := &unblockRecorder{t: t, s: again}
	if err := again.remove("0.9.14", nil, u2.unblock); err != nil {
		t.Fatal(err)
	}
	if got := u2.take(); !slices.EqualFunc(got, [][]string{{"0.9.14"}}, slices.Equal) {
		t.Fatalf("remove unblocked %v, want [[0.9.14]]", got)
	}
	if names := dirNames(t, s.dir); names != "" {
		t.Fatalf("entries = %v, want none", names)
	}
}

func TestFirmwareStoreFailedEvictionNeverLeavesOnlyThePreReplaceCopy(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.1"}), "test")
	img := replaceWithStuckAside(t, s, "0.9.1", u)
	for i := 2; i <= firmwareKept+1; i++ {
		_ = putFWUnblock(t, s, fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", i)}), false, u)
	}
	if got := u.take(); got != nil {
		t.Fatalf("failed eviction unblocked %v", got)
	}
	again := reloadFW(t, s)
	if m, ok := again.get("0.9.1"); !ok || m.SHA256 != img.SHA256 {
		t.Fatalf("after restart 0.9.1 = %+v %v, want the replacement %s", m, ok, img.SHA256)
	}
}

func TestFirmwareStoreEvictionRemovesALeftoverOldCopy(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.1"}), "test")
	replaceWithStuckAside(t, s, "0.9.1", u)
	s.removeAll = os.RemoveAll
	for i := 2; i <= firmwareKept+1; i++ {
		if err := putFWUnblock(t, s, fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", i)}), false, u); err != nil {
			t.Fatal(err)
		}
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.1"}}, slices.Equal) {
		t.Fatalf("eviction unblocked %v, want [[0.9.1]]", got)
	}
	for _, name := range strings.Split(dirNames(t, s.dir), ",") {
		if v, ok := asideVersion(name); name == "0.9.1" || (ok && v == "0.9.1") {
			t.Fatalf("evicted version left %s", name)
		}
	}
	if _, ok := reloadFW(t, s).get("0.9.1"); ok {
		t.Fatal("evicted version came back at boot")
	}
}

func TestFirmwareStoreReplaceRollsBackWhenTheNewCopyCannotMoveIn(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	old, _ := putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	other := fakeFirmware(fwOpts{version: "0.9.14", seed: 9})
	d, _ := parseFirmwareImage(other)
	s.rename = func(oldpath, newpath string) error {
		if strings.HasPrefix(filepath.Base(oldpath), firmwareTempPrefix) {
			return errors.New("injected failure")
		}
		return os.Rename(oldpath, newpath)
	}
	if _, created, err := s.put(d, other, "test", true, nil, nil, u.unblock); err == nil || created {
		t.Fatalf("put = %v %v, want a failure", created, err)
	}
	if m, ok := s.get("0.9.14"); !ok || m.SHA256 != old.SHA256 || binSHA(t, s, "0.9.14") != old.SHA256 {
		t.Fatalf("old version not kept: %+v %v", m, ok)
	}
	if got := u.take(); got != nil {
		t.Fatalf("failed replace unblocked %v", got)
	}
	if names := dirNames(t, s.dir); names != "0.9.14" {
		t.Fatalf("entries = %v", names)
	}
}

func failRollback(s *firmwareStore) {
	s.rename = func(oldpath, newpath string) error {
		base := filepath.Base(oldpath)
		if strings.HasPrefix(base, firmwareTempPrefix) || strings.HasPrefix(base, firmwareAsidePrefix) {
			return errors.New("injected failure")
		}
		return os.Rename(oldpath, newpath)
	}
}

func TestFirmwareStoreReplaceWhoseRollbackFailsHidesTheVersionUntilBootRestoresIt(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	old, _ := putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	other := fakeFirmware(fwOpts{version: "0.9.14", seed: 9})
	d, _ := parseFirmwareImage(other)
	failRollback(s)
	if _, created, err := s.put(d, other, "test", true, nil, nil, u.unblock); err == nil || created {
		t.Fatalf("put = %v %v, want a failure", created, err)
	}
	if m, ok := s.get("0.9.14"); ok {
		t.Fatalf("index serves a version with no dir: %+v", m)
	}
	if got := u.take(); got != nil {
		t.Fatalf("failed replace unblocked %v", got)
	}
	again := reloadFW(t, s)
	if m, ok := again.get("0.9.14"); !ok || m.SHA256 != old.SHA256 || binSHA(t, again, "0.9.14") != old.SHA256 {
		t.Fatalf("old version not restored at boot: %+v %v", m, ok)
	}
}

func TestFirmwareStoreReplaceRetriedAfterAFailedRollbackUnblocks(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	other := fakeFirmware(fwOpts{version: "0.9.14", seed: 9})
	d, _ := parseFirmwareImage(other)
	failRollback(s)
	if _, _, err := s.put(d, other, "test", true, nil, nil, u.unblock); err == nil {
		t.Fatal("put succeeded")
	}
	s.rename = os.Rename
	img, created, err := s.put(d, other, "test", true, nil, nil, u.unblock)
	if err != nil || !created {
		t.Fatalf("retry = %v %v", created, err)
	}
	if got := u.take(); !slices.EqualFunc(got, [][]string{{"0.9.14"}}, slices.Equal) {
		t.Fatalf("retry unblocked %v, want [[0.9.14]]", got)
	}
	if m, ok := reloadFW(t, s).get("0.9.14"); !ok || m.SHA256 != img.SHA256 {
		t.Fatalf("after restart 0.9.14 = %+v %v, want the retried bytes", m, ok)
	}
}

func TestFirmwareStoreDeleteOfAnUnindexedTargetIsRefused(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	dir := s.versionDir("0.9.14")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.remove("0.9.14", func(string) bool { return true }, u.unblock); !errors.Is(err, errFirmwareInUse) {
		t.Fatalf("remove = %v, want in use", err)
	}
	if got := u.take(); got != nil {
		t.Fatalf("refused remove unblocked %v", got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("targeted dir purged: %v", err)
	}
}

func TestFirmwareStoreUploadOverAnUnindexedVersionNeverLeavesOnlyThePreReplaceCopy(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	a, _ := putFW(t, s, fakeFirmware(fwOpts{version: "0.9.14"}), "test")
	b := replaceWithStuckAside(t, s, "0.9.14", u)
	delete(s.index, "0.9.14")
	s.rename = func(oldpath, newpath string) error {
		if strings.HasPrefix(filepath.Base(oldpath), firmwareTempPrefix) {
			return errors.New("injected failure")
		}
		return os.Rename(oldpath, newpath)
	}
	c := fakeFirmware(fwOpts{version: "0.9.14", seed: 11})
	d, _ := parseFirmwareImage(c)
	if _, created, err := s.put(d, c, "test", false, nil, nil, u.unblock); err == nil || created {
		t.Fatalf("upload with a stuck old copy = %v %v, want a failure", created, err)
	}
	if m, ok := reloadFW(t, s).get("0.9.14"); !ok || m.SHA256 != b.SHA256 {
		t.Fatalf("after restart 0.9.14 = %+v %v, want B %s (A is %s)", m, ok, b.SHA256, a.SHA256)
	}
	s.removeAll = os.RemoveAll
	if _, _, err := s.put(d, c, "test", false, nil, nil, u.unblock); err == nil {
		t.Fatal("upload with a failing rename succeeded")
	}
	if got := u.take(); got != nil {
		t.Fatalf("failed uploads unblocked %v", got)
	}
	if m, ok := reloadFW(t, s).get("0.9.14"); ok && m.SHA256 == a.SHA256 {
		t.Fatalf("after restart the pre-replace bytes A loaded: %+v", m)
	}
}

func TestFirmwareStoreFailedEvictionKeepsTheReplacementIndexed(t *testing.T) {
	s := newFWStore(t)
	u := &unblockRecorder{t: t, s: s}
	a, _ := putFW(t, s, fakeFirmware(fwOpts{version: "0.9.1"}), "test")
	b := replaceWithStuckAside(t, s, "0.9.1", u)
	for i := 2; i <= firmwareKept+1; i++ {
		_ = putFWUnblock(t, s, fakeFirmware(fwOpts{version: fmt.Sprintf("0.9.%d", i)}), false, u)
	}
	if m, ok := s.get("0.9.1"); !ok || m.SHA256 != b.SHA256 {
		t.Fatalf("0.9.1 after a failed eviction = %+v %v, want B indexed", m, ok)
	}
	c := fakeFirmware(fwOpts{version: "0.9.1", seed: 11})
	d, _ := parseFirmwareImage(c)
	if _, _, err := s.put(d, c, "test", false, nil, nil, u.unblock); !errors.Is(err, errFirmwareConflict) {
		t.Fatalf("plain upload over it = %v, want conflict", err)
	}
	if got := u.take(); got != nil {
		t.Fatalf("unblocked %v", got)
	}
	if m, ok := reloadFW(t, s).get("0.9.1"); !ok || m.SHA256 == a.SHA256 {
		t.Fatalf("after restart 0.9.1 = %+v %v, want not A", m, ok)
	}
}
