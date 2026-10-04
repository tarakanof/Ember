package producer

import (
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	line := []byte("4242 (ember-claude-pr) S 4100 4242 4100 34816 4242 4194304 120 0 0 0 1 0 0 0 20 0 1 0 987654 1234567 300 18446744073709551615\n")
	st, ok := ParseProcStat(line)
	if !ok || st.PPID != 4100 || st.Comm != "ember-claude-pr" || st.StartTicks != 987654 {
		t.Fatalf("got %+v, %v", st, ok)
	}
}

func TestParseProcStatCommWithSpacesAndParens(t *testing.T) {
	line := []byte("7 (a) b (c) R 1 7 7 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 55 0 0\n")
	st, ok := ParseProcStat(line)
	if !ok || st.Comm != "a) b (c" || st.PPID != 1 || st.StartTicks != 55 {
		t.Fatalf("got %+v, %v", st, ok)
	}
}

func TestParseProcStatRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "1 (x) S 1", "no parens here"} {
		if _, ok := ParseProcStat([]byte(s)); ok {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestParseBootTime(t *testing.T) {
	got, ok := ParseBootTime([]byte("cpu  1 2 3\nbtime 1700000000\nprocesses 9\n"))
	if !ok || !got.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("got %v, %v", got, ok)
	}
}
