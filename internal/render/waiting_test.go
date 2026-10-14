package render

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"image/gif"
	"strings"
	"testing"
	"time"
)

func projectSnap(sessions ...Session) Snapshot {
	now := time.Unix(1_000_000, 0)
	for i := range sessions {
		sessions[i].UpdatedAt = now
		sessions[i].StateSince = now.Add(-time.Duration(i) * time.Second)
	}
	return Snapshot{Now: now, Sessions: sessions}
}

func waiter(source, tool, sess, project string) Session {
	return Session{Source: source, Tool: tool, Session: sess, State: "waiting", Project: project}
}

func TestLockedWaitScrollsTheProjectInALoopAndBlinksTheLogo(t *testing.T) {
	snap := projectSnap(waiter("m4", "claude", "s1", "learn-language"))
	p := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, true, 30, nil, CoordOpts{WaitIcons: true})
	if p["text"] != "learn-language" {
		t.Fatalf("text = %v", p["text"])
	}
	if p["icon"] != WaitIconName("claude") {
		t.Fatalf("icon = %v, want the blinking amber logo", p["icon"])
	}
	if _, ok := p["textBlinkMs"]; ok {
		t.Fatal("the logo blinks, not the name")
	}
	if _, ok := p["textOffsetX"]; ok {
		t.Fatal("a native icon already indents the text")
	}
	scroll, _ := p["scroll"].(map[string]any)
	if scroll["mode"] != "loop" {
		t.Fatalf("scroll = %v, want a loop while the lock holds", p["scroll"])
	}
	if p["durationMs"] != p["lifetimeMs"] {
		t.Fatal("the locked frame must hold")
	}
	if draw, ok := p["draw"].([]any); ok {
		for _, op := range draw {
			if op.([]any)[1] == 0 {
				t.Fatalf("a drawn op at col 0 would paint over the native icon: %v", op)
			}
		}
	}
	if errs := CheckNGPayload(p, false); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestLockedWaitBlinksTheNameUntilTheIconsAreOnTheClock(t *testing.T) {
	snap := projectSnap(waiter("m4", "codex", "s1", "ember"))
	p := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, true, 30, nil, CoordOpts{})
	if p["text"] != "ember" || p["textBlinkMs"] != 500 {
		t.Fatalf("payload = %v", p)
	}
	if _, ok := p["icon"]; ok {
		t.Fatal("no native icon before it is provisioned")
	}
}

func TestDisplayNamePrefixesTheHostOnlyWithSeveralHosts(t *testing.T) {
	snap := projectSnap(waiter("m5", "claude", "s1", "learn-language"), waiter("m4", "codex", "s2", "ember"))
	p := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, true, 30, nil, CoordOpts{})
	if p["text"] != "M5/learn-language +1" {
		t.Fatalf("text = %v", p["text"])
	}
}

func TestLockedWaitWithoutAProjectKeepsTheHostLabel(t *testing.T) {
	snap := projectSnap(waiter("m4", "claude", "s1", ""))
	p := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, true, 30, nil, CoordOpts{WaitIcons: true})
	if p["text"] != "WAIT M4" {
		t.Fatalf("text = %v", p["text"])
	}
	if _, ok := p["icon"]; ok {
		t.Fatal("an old producer's wait keeps the drawn icon")
	}
}

func TestWaitingCardInRotationScrollsTheProjectOnce(t *testing.T) {
	snap := projectSnap(waiter("m4", "claude", "s1", "learn-language"))
	p := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, false, 30, nil, CoordOpts{WaitIcons: true})
	if p["text"] != "learn-language" || p["repeat"] != 1 {
		t.Fatalf("payload = %v", p)
	}
	if p["durationMs"] == p["lifetimeMs"] {
		t.Fatal("the rotation card must not hold")
	}
	if errs := CheckNGPayload(p, false); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestWaitingWithoutAProjectHasNoProjectCard(t *testing.T) {
	if got := AvailableCards(waiter("m4", "claude", "s1", ""), nil); len(got) != 1 || got[0] != cardSource {
		t.Fatalf("cards = %v", got)
	}
}

func TestWaitIconsBlinkEveryHalfSecond(t *testing.T) {
	icons, err := WaitIcons()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{WaitIconName("claude"), WaitIconName("codex"), WaitIconName("t3")} {
		g, err := gif.DecodeAll(bytes.NewReader(icons[name]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(g.Image) != 2 || g.Delay[0] != 50 || g.Delay[1] != 50 || g.LoopCount != 0 {
			t.Fatalf("%s: frames %d delays %v loop %d", name, len(g.Image), g.Delay, g.LoopCount)
		}
		if b := g.Image[0].Bounds(); b.Dx() != 8 || b.Dy() != 8 {
			t.Fatalf("%s: %v", name, b)
		}
		lit := 0
		for _, px := range g.Image[0].Pix {
			if px != 0 {
				lit++
			}
		}
		if lit == 0 {
			t.Fatalf("%s: the on frame is blank", name)
		}
		for _, px := range g.Image[1].Pix {
			if px != 0 {
				t.Fatalf("%s: the off frame is not blank", name)
			}
		}
	}
}

func TestWaitIconNamesCarryTheirContentHash(t *testing.T) {
	icons, err := WaitIcons()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range icons {
		if want := fmt.Sprintf("_%08x", crc32.ChecksumIEEE(data)); !strings.HasSuffix(name, want) {
			t.Fatalf("%s: want suffix %s, so a changed GIF gets a new name and is uploaded again", name, want)
		}
	}
	if WaitIconName("gemini") != WaitIconName("claude") {
		t.Fatal("unknown tools use the Claude logo")
	}
}

func TestWaitingLabelCountsTheOtherWaiters(t *testing.T) {
	snap := projectSnap(waiter("m4", "claude", "s1", "learn-language"), waiter("m4", "codex", "s2", "ember"),
		waiter("m4", "claude", "s3", ""), Session{Source: "m4", Tool: "claude", Session: "s4", State: "running"})
	p := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, true, 30, nil, CoordOpts{})
	if p["text"] != "learn-language +2" {
		t.Fatalf("text = %v, want the lead plus the 2 other waiting sessions", p["text"])
	}
	card := RenderForCoordWith(snap, snap.Sessions[0].Key(), 0, false, 30, nil, CoordOpts{})
	if card["text"] != "learn-language +2" {
		t.Fatalf("card text = %v", card["text"])
	}
}
