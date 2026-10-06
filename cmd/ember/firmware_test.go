package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	got, created, err := s.put(d, img, channel, false, nil, nil)
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
	if _, _, err := s.put(d, other, "test", false, nil, nil); !errors.Is(err, errFirmwareConflict) {
		t.Fatalf("other bytes err = %v, want conflict", err)
	}
	if _, _, err := s.put(d, other, "test", true, func(string) bool { return true }, nil); !errors.Is(err, errFirmwareInUse) {
		t.Fatalf("replace of a target err = %v, want in use", err)
	}
	replaced, created, err := s.put(d, other, "test", true, func(string) bool { return false }, nil)
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
		if _, _, err := s.put(d, img, "test", false, nil, func(v string) bool { return v == "0.9.1" }); err != nil {
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
	if err := s.remove("0.9.14", func(string) bool { return true }); !errors.Is(err, errFirmwareInUse) {
		t.Fatalf("remove target err = %v", err)
	}
	if err := s.remove("0.9.14", func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := s.remove("0.9.14", func(string) bool { return false }); !errors.Is(err, errFirmwareNotFound) {
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
	if _, _, err := s.put(d, other, "test", true, func(string) bool { return false }, nil); err == nil {
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
