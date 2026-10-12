package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	demoPickup    = 10 * time.Second
	demoGrace     = 5 * time.Second
	demoSilence   = 60 * time.Second
	demoHoldCap   = 60 * time.Second
	demoForget    = 60 * time.Second
	demoMaxScenes = 256

	demoIdle       = "idle"
	demoPending    = "pending"
	demoPlaying    = "playing"
	demoUnanswered = "unanswered"
	demoDone       = "done"
)

var (
	errDemoUnsupported = errors.New("the knob's firmware does not report the demo feature")
	errDemoBody        = errors.New(`send "scene" or a non-empty "scenes", not both`)
)

type knobDemo struct {
	ID      string    `json:"id"`
	Page    string    `json:"page"`
	View    *knobView `json:"view,omitempty"`
	Act     string    `json:"act,omitempty"`
	Offline bool      `json:"offline,omitempty"`
	Hold    bool      `json:"hold"`
}

type demoStatus struct {
	State     string `json:"state"`
	Scene     string `json:"scene"`
	ID        string `json:"id"`
	QueueLeft int    `json:"queue_left"`
}

type demoRun struct {
	queue  []string
	id     string
	hold   bool
	state  string
	served bool
	since  time.Time
	played time.Time
	heard  time.Time
}

type demoCurrent struct {
	scene string
	id    string
	hold  bool
	since time.Time
}

type demoRegistry struct {
	mu    sync.Mutex
	runs  map[string]*demoRun
	newID func() string
}

func randomDemoID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (d *demoRegistry) nextIDLocked() string {
	if d.newID != nil {
		return d.newID()
	}
	return randomDemoID()
}

func (d *demoRegistry) start(device string, scenes []string, hold bool, now time.Time) demoStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.runs == nil {
		d.runs = map[string]*demoRun{}
	}
	run := &demoRun{queue: scenes, id: d.nextIDLocked(), hold: hold, state: demoPending, since: now}
	d.runs[device] = run
	return run.status()
}

func (d *demoRegistry) stop(device string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.runs, device)
}

func (d *demoRegistry) status(device string, now time.Time) demoStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	run := d.liveLocked(device, now)
	if run == nil {
		return demoStatus{State: demoIdle}
	}
	return run.status()
}

func (d *demoRegistry) current(device string, now time.Time) (demoCurrent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	run := d.liveLocked(device, now)
	if run == nil || run.state == demoUnanswered {
		return demoCurrent{}, false
	}
	run.served = true
	return demoCurrent{scene: run.queue[0], id: run.id, hold: run.hold, since: run.since}, true
}

func (d *demoRegistry) report(device, id, state string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	run := d.liveLocked(device, now)
	if run == nil || run.id != id {
		return false
	}
	if run.state == demoUnanswered && !(state == demoPlaying && run.served && now.Sub(run.since) < demoPickup+demoGrace) {
		return false
	}
	switch state {
	case demoPlaying:
		run.heard = now
		if run.state == demoPlaying {
			return false
		}
		run.state, run.played = demoPlaying, now
		return true
	case demoDone:
		if len(run.queue) <= 1 {
			delete(d.runs, device)
			return true
		}
		run.queue = run.queue[1:]
		run.id = d.nextIDLocked()
		run.state, run.served = demoPending, false
		run.since, run.played, run.heard = now, time.Time{}, time.Time{}
		return true
	}
	return false
}

func (d *demoRegistry) liveLocked(device string, now time.Time) *demoRun {
	run := d.runs[device]
	if run == nil {
		return nil
	}
	if run.state == demoPending && now.Sub(run.since) >= demoPickup {
		run.state = demoUnanswered
	}
	switch {
	case run.state == demoUnanswered && now.Sub(run.since) >= demoPickup+demoForget,
		run.state == demoPlaying && now.Sub(run.heard) >= demoSilence,
		run.state == demoPlaying && run.hold && now.Sub(run.played) >= demoHoldCap:
		delete(d.runs, device)
		return nil
	}
	return run
}

func (r *demoRun) status() demoStatus {
	return demoStatus{State: r.state, Scene: r.queue[0], ID: r.id, QueueLeft: len(r.queue) - 1}
}

func (a *App) knobDemo(id string, base knobView, now time.Time) *knobDemo {
	cur, ok := a.demos.current(id, now)
	if !ok {
		return nil
	}
	sc := demoSceneByName[cur.scene]
	art, _ := demoArtOnce()
	d := &knobDemo{ID: cur.id, Page: sc.page, Act: sc.act, Offline: sc.offline, Hold: cur.hold}
	if sc.view != nil {
		v := sc.view(cur.since.Truncate(time.Second), art)
		v.V, v.Epoch, v.ConfigVersion, v.Brightness = base.V, base.Epoch, base.ConfigVersion, base.Brightness
		d.View = &v
	}
	return d
}

func (a *App) demoKnob(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	st, err := a.devices.viewState(id)
	if err != nil {
		a.writeDeviceError(w, r, err)
		return "", false
	}
	if !st.caps.hasFeature(featureDemo) {
		writeError(w, http.StatusConflict, errDemoUnsupported)
		return "", false
	}
	return id, true
}

func (a *App) handleDemoStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scene  string   `json:"scene"`
		Scenes []string `json:"scenes"`
		Hold   bool     `json:"hold"`
	}
	if !a.decodeOrReject(w, r, &req, true) {
		return
	}
	id, ok := a.demoKnob(w, r)
	if !ok {
		return
	}
	scenes := req.Scenes
	if (req.Scene == "") == (len(scenes) == 0) {
		writeError(w, http.StatusBadRequest, errDemoBody)
		return
	}
	hold := req.Hold && req.Scenes == nil
	if req.Scene != "" {
		scenes = []string{req.Scene}
	}
	if len(scenes) > demoMaxScenes {
		writeError(w, http.StatusBadRequest, fmt.Errorf("at most %d scenes", demoMaxScenes))
		return
	}
	for _, s := range scenes {
		if _, ok := demoSceneByName[s]; !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unknown scene %q", s))
			return
		}
	}
	st := a.demos.start(id, scenes, hold, a.viewNow())
	a.changes.notify(topicDemo)
	writeJSON(w, http.StatusOK, st)
}

func (a *App) handleDemoStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.devices.viewState(id); err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	a.demos.stop(id)
	a.changes.notify(topicDemo)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleDemoStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.devices.viewState(id); err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.demos.status(id, a.viewNow()))
}

func (a *App) handleDemoScenes(w http.ResponseWriter, r *http.Request) {
	if _, err := a.devices.viewState(r.PathValue("id")); err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, demoCatalog)
}

func (a *App) noteDemoReport(id string, r *http.Request) {
	q := r.URL.Query()
	demoID := q.Get("demo_id")
	if demoID == "" {
		return
	}
	if a.demos.report(id, demoID, q.Get("demo_state"), a.viewNow()) {
		a.changes.notify(topicDemo)
	}
}
