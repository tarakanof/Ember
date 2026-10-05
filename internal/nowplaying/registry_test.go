package nowplaying

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func song(source, player string, st State, title string, pos int64) Report {
	return Report{Source: source, Player: player, State: st, Title: title, Artist: "A", Album: "B",
		TrackID: title, DurationMS: 200_000, PositionMS: pos}
}

func mustReport(t *testing.T, r *Registry, rep Report, now time.Time) bool {
	t.Helper()
	changed, err := r.Report(rep, now)
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

func TestCurrentPrefersNewestPlaying(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Playing, "one", 0), t0)
	mustReport(t, r, song("music", "mbp", Playing, "two", 0), t0.Add(time.Second))
	mustReport(t, r, song("plex", "tv", Paused, "three", 0), t0.Add(2*time.Second))
	e, ok := r.Current(t0.Add(3 * time.Second))
	if !ok || e.Title != "two" {
		t.Fatalf("current = %q %v, want two", e.Title, ok)
	}
}

func TestCurrentFallsBackToPausedUntilTTL(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "mbp", Paused, "one", 5000), t0)
	if e, ok := r.Current(t0.Add(PausedTTL - time.Second)); !ok || e.Title != "one" {
		t.Fatalf("paused entry should show before its TTL")
	}
	if _, ok := r.Current(t0.Add(PausedTTL)); ok {
		t.Fatal("paused entry should age out after 10 min")
	}
}

func TestRepeatedPausedReportsDoNotExtendTTL(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Paused, "one", 5000), t0)
	mustReport(t, r, song("plex", "amp", Paused, "one", 5000), t0.Add(9*time.Minute))
	if _, ok := r.Current(t0.Add(PausedTTL)); ok {
		t.Fatal("re-reporting paused must not restart the 10 min clock")
	}
}

func TestPlayingExpiresAfterTrackEndPlusGrace(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "mbp", Playing, "one", 190_000), t0)
	if _, ok := r.Current(t0.Add(10*time.Second + PlayingGrace)); !ok {
		t.Fatal("should still show within the grace")
	}
	if _, ok := r.Current(t0.Add(11*time.Second + PlayingGrace)); ok {
		t.Fatal("a playing entry past its end + grace should expire")
	}
}

func TestPlayingWithoutDurationExpiresAfter30Min(t *testing.T) {
	r := NewRegistry()
	rep := song("music", "mbp", Playing, "stream", 0)
	rep.DurationMS = 0
	mustReport(t, r, rep, t0)
	if _, ok := r.Current(t0.Add(NoDurationTTL)); ok {
		t.Fatal("expected expiry")
	}
}

func TestStoppedRemovesPlayer(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Playing, "one", 0), t0)
	mustReport(t, r, Report{Source: "plex", Player: "amp", State: Stopped}, t0)
	if _, ok := r.Current(t0); ok {
		t.Fatal("stopped should remove the entry")
	}
}

func TestConsistentPositionKeepsAnchor(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Playing, "one", 10_000), t0)
	mustReport(t, r, song("plex", "amp", Playing, "one", 12_500), t0.Add(2*time.Second))
	e, _ := r.Current(t0.Add(2 * time.Second))
	if !e.PositionAt.Equal(t0) || e.PositionMS != 10_000 {
		t.Fatalf("anchor moved: %v %d", e.PositionAt, e.PositionMS)
	}
	if got := e.Position(t0.Add(2 * time.Second)); got != 12_000 {
		t.Fatalf("position = %d, want 12000", got)
	}
}

func TestSeekMovesAnchor(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Playing, "one", 10_000), t0)
	mustReport(t, r, song("plex", "amp", Playing, "one", 90_000), t0.Add(2*time.Second))
	e, _ := r.Current(t0.Add(2 * time.Second))
	if e.PositionMS != 90_000 || !e.PositionAt.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("seek not anchored: %d at %v", e.PositionMS, e.PositionAt)
	}
}

func TestPauseReanchorsAndStopsExtrapolating(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Playing, "one", 10_000), t0)
	mustReport(t, r, song("plex", "amp", Paused, "one", 15_000), t0.Add(5*time.Second))
	e, _ := r.Current(t0.Add(time.Minute))
	if e.Position(t0.Add(time.Minute)) != 15_000 {
		t.Fatalf("paused position moved: %d", e.Position(t0.Add(time.Minute)))
	}
}

func TestNewTrackDropsArtAndReportsChange(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "mbp", Playing, "one", 0), t0)
	if err := r.SetArt("music", "mbp", "one", Album, NewImage([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	if mustReport(t, r, song("music", "mbp", Playing, "one", 1000), t0.Add(time.Second)) {
		t.Fatal("same track reported as a change")
	}
	e, _ := r.Current(t0.Add(time.Second))
	if e.AlbumArt == nil {
		t.Fatal("same track lost its art")
	}
	if !mustReport(t, r, song("music", "mbp", Playing, "two", 0), t0.Add(2*time.Second)) {
		t.Fatal("new track not reported as a change")
	}
	e, _ = r.Current(t0.Add(2 * time.Second))
	if e.AlbumArt != nil || e.ArtVersion() != "" {
		t.Fatal("new track kept the old art")
	}
}

func TestSetArtRejectsOtherTrack(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "mbp", Playing, "two", 0), t0)
	err := r.SetArt("music", "mbp", "one", Album, NewImage([]byte("x")))
	if !errors.Is(err, ErrTrackMismatch) {
		t.Fatalf("err = %v, want ErrTrackMismatch", err)
	}
	if err := r.SetArt("plex", "x", "", Album, NewImage([]byte("x"))); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("err = %v, want ErrNoEntry", err)
	}
}

func TestArtVersionFollowsPictures(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "mbp", Playing, "one", 0), t0)
	_ = r.SetArt("music", "mbp", "", Album, NewImage([]byte("a")))
	e1, _ := r.Current(t0)
	_ = r.SetArt("music", "mbp", "", Artist, NewImage([]byte("b")))
	e2, _ := r.Current(t0)
	if e1.ArtVersion() == "" || e1.ArtVersion() == e2.ArtVersion() {
		t.Fatalf("art_version did not move: %q %q", e1.ArtVersion(), e2.ArtVersion())
	}
	if e2.Picture(Backdrop) != e2.ArtistArt {
		t.Fatal("backdrop should use the artist picture")
	}
	if e1.Picture(Backdrop) != e1.AlbumArt {
		t.Fatal("backdrop should fall back to the album")
	}
}

func TestValidateRejectsBadReports(t *testing.T) {
	for name, rep := range map[string]Report{
		"source":   {Source: "Plex!", State: Playing},
		"state":    {Source: "plex", State: "rewinding"},
		"title":    {Source: "plex", State: Playing, Title: strings.Repeat("x", 201)},
		"negative": {Source: "plex", State: Playing, PositionMS: -1},
	} {
		if err := rep.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	ok := Report{Source: "music", State: Playing, Title: strings.Repeat("é", 200)}
	if err := ok.Validate(); err != nil {
		t.Fatalf("200 multibyte characters rejected: %v", err)
	}
}

func TestSetArtistArtOnlyForSameArtistWithoutPicture(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "mbp", Playing, "one", 0), t0)
	if r.SetArtistArt("music", "mbp", "Other", NewImage([]byte("x"))) {
		t.Fatal("attached to another artist")
	}
	if !r.SetArtistArt("music", "mbp", "A", NewImage([]byte("x"))) {
		t.Fatal("not attached")
	}
	if r.SetArtistArt("music", "mbp", "A", NewImage([]byte("y"))) {
		t.Fatal("replaced an existing picture")
	}
	if e, ok := r.Get("music", "mbp"); !ok || e.ArtistArt == nil {
		t.Fatal("Get lost the picture")
	}
}

func TestExpiredPausedStaysHiddenWhileReReported(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("plex", "amp", Paused, "one", 5000), t0)
	for m := 1; m <= 20; m++ {
		at := t0.Add(time.Duration(m) * time.Minute)
		mustReport(t, r, song("plex", "amp", Paused, "one", 5000), at)
		if _, ok := r.Current(at); ok && m >= 10 {
			t.Fatalf("paused track came back after %d min of re-reports", m)
		}
	}
	mustReport(t, r, song("plex", "amp", Playing, "one", 5000), t0.Add(21*time.Minute))
	if _, ok := r.Current(t0.Add(21 * time.Minute)); !ok {
		t.Fatal("resuming should show the track again")
	}
}

func TestRegistryForgetsSilentEntriesAndCapsPlayers(t *testing.T) {
	r := NewRegistry()
	mustReport(t, r, song("music", "old", Paused, "x", 0), t0)
	mustReport(t, r, song("music", "new", Paused, "y", 0), t0.Add(ForgetAfter))
	if _, ok := r.Get("music", "old"); ok {
		t.Fatal("silent entry not forgotten")
	}
	for i := 0; i < maxEntries+5; i++ {
		mustReport(t, r, song("music", strings.Repeat("p", i+1), Playing, "z", 0), t0.Add(ForgetAfter+time.Duration(i)*time.Second))
	}
	r.mu.Lock()
	n := len(r.entries)
	r.mu.Unlock()
	if n > maxEntries {
		t.Fatalf("entries = %d, cap %d", n, maxEntries)
	}
}

func TestOnChangeSkipsHeartbeats(t *testing.T) {
	r := NewRegistry()
	n := 0
	r.OnChange = func() { n++ }
	step := func(what string, want int, f func()) {
		t.Helper()
		before := n
		f()
		if got := n - before; got != want {
			t.Fatalf("%s: %d OnChange calls, want %d", what, got, want)
		}
	}
	step("new track", 1, func() { mustReport(t, r, song("s", "p", Playing, "one", 1000), t0) })
	step("heartbeat on time", 0, func() { mustReport(t, r, song("s", "p", Playing, "one", 3000), t0.Add(2*time.Second)) })
	step("seek", 1, func() { mustReport(t, r, song("s", "p", Playing, "one", 90_000), t0.Add(3*time.Second)) })
	step("pause", 1, func() { mustReport(t, r, song("s", "p", Paused, "one", 90_500), t0.Add(4*time.Second)) })
	step("album art", 1, func() {
		if err := r.SetArt("s", "p", "one", Album, &Image{Hash: "h"}); err != nil {
			t.Fatal(err)
		}
	})
	step("artist art", 1, func() { r.SetArtistArt("s", "p", "A", &Image{Hash: "a"}) })
	step("artist art again: refused", 0, func() { r.SetArtistArt("s", "p", "A", &Image{Hash: "b"}) })
	step("stopped", 1, func() { mustReport(t, r, song("s", "p", Stopped, "one", 0), t0.Add(5*time.Second)) })
	step("remove unknown", 0, func() { r.Remove("s", "p") })
}

func TestVolumeIsValidatedAndAChange(t *testing.T) {
	vol := func(v int) *int { return &v }
	bad := song("music", "mbp", Playing, "one", 0)
	bad.Volume = vol(101)
	if _, err := NewRegistry().Report(bad, t0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("volume 101: err = %v", err)
	}
	r := NewRegistry()
	changes := 0
	r.OnChange = func() { changes++ }
	rep := song("music", "mbp", Playing, "one", 0)
	rep.Volume = vol(40)
	mustReport(t, r, rep, t0)
	rep.Volume = vol(40) // another pointer, same value: a heartbeat
	mustReport(t, r, rep, t0.Add(time.Second))
	if changes != 1 {
		t.Fatalf("same volume: %d changes, want 1", changes)
	}
	rep.Volume = vol(45)
	mustReport(t, r, rep, t0.Add(2*time.Second))
	if e, _ := r.Get("music", "mbp"); changes != 2 || e.Volume == nil || *e.Volume != 45 {
		t.Fatalf("new volume: %d changes, entry %v", changes, e.Volume)
	}
	rep.Volume = nil // unknown this time: keeps 45, no change
	mustReport(t, r, rep, t0.Add(3*time.Second))
	if e, _ := r.Get("music", "mbp"); changes != 2 || e.Volume == nil || *e.Volume != 45 {
		t.Fatalf("no volume: %d changes, entry %v", changes, e.Volume)
	}
}
