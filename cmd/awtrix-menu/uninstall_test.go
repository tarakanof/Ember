package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadPlistBinary_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.plist")
	plist := generatePlist("/Users/joe/go/bin/awtrix-menu", "/Users/joe", 501)
	if err := os.WriteFile(path, plist, 0o644); err != nil {
		t.Fatal(err)
	}
	got := readPlistBinary(path)
	if got != "/Users/joe/go/bin/awtrix-menu" {
		t.Errorf("readPlistBinary = %q, want %q", got, "/Users/joe/go/bin/awtrix-menu")
	}
}

func TestReadPlistBinary_MissingFile(t *testing.T) {
	if got := readPlistBinary("/nonexistent/plist.xml"); got != "" {
		t.Errorf("missing file → %q, want \"\"", got)
	}
}

func TestDistinctNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "a"}, []string{"a"}},
		{[]string{"", "a", ""}, []string{"a"}},
		{[]string{"a", "", "b", "a"}, []string{"a", "b"}},
		{[]string{}, nil},
	}
	for _, tc := range cases {
		got := distinctNonEmpty(tc.in...)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("distinctNonEmpty(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
