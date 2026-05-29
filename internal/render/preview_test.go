package render

import "testing"

func TestSampleBaseSession(t *testing.T) {
	s := SampleBaseSession()
	if s.Source != "mbp" || s.Tool != "claude" || s.Session != "sample" || s.State != "running" {
		t.Fatalf("unexpected sample base: %+v", s)
	}
}
