package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/devices"
)

const fullRoom = `{"t_c":23.4,"rh":41.2,"eco2":612,"tvoc":85,"aqi":2,"air":"normal","lux":312,"present":true,"age_s":{"climate":4,"air":1,"light":0,"presence":0}}`

type roomKnob struct {
	app *App
	srv *httptest.Server
	id  string
	tok string
	clk *stepClock
}

func newRoomKnob(t *testing.T, dbPath string) roomKnob {
	t.Helper()
	app, srv := newDevicesApp(t, dbPath)
	clk := &stepClock{now: time.Date(2026, 10, 30, 12, 0, 0, 500_000_000, time.UTC)}
	app.devices.Now = clk.Now
	m := mintKnob(t, srv, http.StatusCreated)
	return roomKnob{app: app, srv: srv, id: m.ID, tok: m.Token, clk: clk}
}

func (k roomKnob) checkin(t *testing.T, body string) {
	t.Helper()
	resp, b := devReq(t, k.srv, "POST", "/v1/devices/self/checkin", k.tok, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkin %s = %d: %s", body, resp.StatusCode, b)
	}
}

func (k roomKnob) checkinRoom(t *testing.T, room string) {
	t.Helper()
	k.checkin(t, `{"fw":"0.10.0","room":`+room+`}`)
}

func (k roomKnob) stored() *devices.Room {
	return k.app.devices.List()[0].LastCheckin.Room
}

func (k roomKnob) roomJSON(t *testing.T) map[string]any {
	t.Helper()
	got := k.stored()
	if got == nil {
		t.Fatal("no room block stored")
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "received_at")
	return m
}

func jsonEqual(t *testing.T, got map[string]any, want string) {
	t.Helper()
	var w map[string]any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("room = %v, want %v", got, w)
	}
}

func TestDeviceCheckinWithoutARoomStoresNone(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkin(t, `{"fw":"0.9.13"}`)
	if got := k.stored(); got != nil {
		t.Fatalf("room = %+v", got)
	}
	resp, b := devReq(t, k.srv, "GET", "/v1/devices", testToken, "")
	if s := string(mustOK(t, "list", resp, b)); strings.Contains(s, `"room"`) {
		t.Fatalf("a knob without a block lists one: %s", s)
	}
}

func TestDeviceCheckinStoresTheFullRoomBlock(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, fullRoom)
	jsonEqual(t, k.roomJSON(t), fullRoom)
	if got, want := k.stored().ReceivedAt, k.clk.Now().Truncate(time.Second); !got.Equal(want) {
		t.Fatalf("received_at = %v, want %v", got, want)
	}
}

func TestDeviceCheckinKeepsZeroAndFalseReadings(t *testing.T) {
	k := newRoomKnob(t, "")
	room := `{"t_c":0,"rh":0,"tvoc":0,"lux":0,"present":false,"age_s":{"climate":0}}`
	k.checkinRoom(t, room)
	jsonEqual(t, k.roomJSON(t), room)
}

func TestDeviceCheckinAcceptsAnySubsetOfTheRoomBlock(t *testing.T) {
	cases := map[string]string{
		"empty":    `{}`,
		"climate":  `{"t_c":23.4,"rh":41.2,"age_s":{"climate":4}}`,
		"air":      `{"eco2":612,"tvoc":85,"aqi":2,"air":"warm_up","age_s":{"air":1}}`,
		"validity": `{"air":"start_up"}`,
		"light":    `{"lux":312.5}`,
		"presence": `{"present":true,"age_s":{"presence":59}}`,
	}
	for name, room := range cases {
		t.Run(name, func(t *testing.T) {
			k := newRoomKnob(t, "")
			k.checkinRoom(t, room)
			jsonEqual(t, k.roomJSON(t), room)
		})
	}
}

func TestDeviceCheckinWithoutARoomKeepsTheLastBlock(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, fullRoom)
	at := k.stored().ReceivedAt
	k.clk.advance(30 * time.Second)
	k.checkin(t, `{"fw":"0.10.0"}`)
	jsonEqual(t, k.roomJSON(t), fullRoom)
	if got := k.stored().ReceivedAt; !got.Equal(at) {
		t.Fatalf("received_at moved to %v without a block", got)
	}
	k.checkin(t, `{"fw":"0.10.0","room":null}`)
	jsonEqual(t, k.roomJSON(t), fullRoom)
}

func TestANewRoomBlockReplacesTheLastOneWhole(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, fullRoom)
	k.clk.advance(30 * time.Second)
	k.checkinRoom(t, `{"present":false}`)
	jsonEqual(t, k.roomJSON(t), `{"present":false}`)
	if got, want := k.stored().ReceivedAt, k.clk.Now().Truncate(time.Second); !got.Equal(want) {
		t.Fatalf("received_at = %v, want %v", got, want)
	}
}

func TestStaleCheckinDoesNotTouchTheRoomBlock(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkin(t, `{"fw":"0.10.0","uptime_s":100,"diag":{"boots":5},"room":{"lux":100}}`)
	at := k.stored().ReceivedAt
	k.clk.advance(5 * time.Second)
	k.checkin(t, `{"fw":"0.10.0","uptime_s":90,"diag":{"boots":5},"room":{"lux":1}}`)
	jsonEqual(t, k.roomJSON(t), `{"lux":100}`)
	if got := k.stored().ReceivedAt; !got.Equal(at) {
		t.Fatalf("received_at moved to %v on a stale checkin", got)
	}
}

func TestRoomBlockSurvivesARotationCheckin(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, fullRoom)
	resp, b := devReq(t, k.srv, "POST", "/v1/devices/"+k.id+"/rotate", testToken, "")
	mustOK(t, "rotate", resp, b)
	k.checkin(t, `{"fw":"0.10.0"}`)
	jsonEqual(t, k.roomJSON(t), fullRoom)
}

func TestRoomBlockSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	k := newRoomKnob(t, path)
	k.checkinRoom(t, fullRoom)
	if err := k.app.devices.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = k.app.store.Close()

	_, srv := newDevicesApp(t, path)
	resp, b := devReq(t, srv, "GET", "/v1/devices/"+k.id+"/room", testToken, "")
	body := mustOK(t, "room", resp, b)
	if !strings.Contains(string(body), `"t_c":23.4`) {
		t.Fatalf("after a restart: %s", body)
	}
}

func TestDeviceCheckinDropsOnlyTheBadRoomValues(t *testing.T) {
	cases := []struct {
		name, room, want string
	}{
		{"cold", `{"t_c":-40.1,"rh":41}`, `{"rh":41}`},
		{"hot", `{"t_c":85.1,"rh":41}`, `{"rh":41}`},
		{"t_c string", `{"t_c":"23","rh":41}`, `{"rh":41}`},
		{"t_c overflow", `{"t_c":1e999,"rh":41}`, `{"rh":41}`},
		{"rh low", `{"rh":-0.1,"lux":5}`, `{"lux":5}`},
		{"rh high", `{"rh":100.1,"lux":5}`, `{"lux":5}`},
		{"eco2 low", `{"eco2":399,"aqi":2}`, `{"aqi":2}`},
		{"eco2 high", `{"eco2":65001,"aqi":2}`, `{"aqi":2}`},
		{"eco2 fraction", `{"eco2":612.5,"aqi":2}`, `{"aqi":2}`},
		{"tvoc low", `{"tvoc":-1,"aqi":2}`, `{"aqi":2}`},
		{"tvoc high", `{"tvoc":65001,"aqi":2}`, `{"aqi":2}`},
		{"aqi low", `{"aqi":0,"tvoc":5}`, `{"tvoc":5}`},
		{"aqi high", `{"aqi":6,"tvoc":5}`, `{"tvoc":5}`},
		{"air unknown", `{"air":"warming","aqi":2}`, `{"aqi":2}`},
		{"air number", `{"air":1,"aqi":2}`, `{"aqi":2}`},
		{"lux low", `{"lux":-1,"aqi":2}`, `{"aqi":2}`},
		{"lux high", `{"lux":125001,"aqi":2}`, `{"aqi":2}`},
		{"present number", `{"present":1,"aqi":2}`, `{"aqi":2}`},
		{"age not an object", `{"age_s":4,"aqi":2}`, `{"aqi":2}`},
		{"one age negative", `{"age_s":{"climate":-1,"air":1},"aqi":2}`, `{"age_s":{"air":1},"aqi":2}`},
		{"one age string", `{"age_s":{"light":"0","air":1},"aqi":2}`, `{"age_s":{"air":1},"aqi":2}`},
		{"only age negative", `{"age_s":{"presence":-1},"aqi":2}`, `{"aqi":2}`},
		{"every value bad", `{"t_c":999,"rh":999,"eco2":0,"tvoc":-1,"aqi":9,"air":"x","lux":-1,"present":"y","age_s":[]}`, `{}`},
		{"unknown key", `{"pressure":1013,"aqi":2}`, `{"aqi":2}`},
		{"null value", `{"t_c":null,"aqi":2}`, `{"aqi":2}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			k := newRoomKnob(t, "")
			k.checkinRoom(t, c.room)
			jsonEqual(t, k.roomJSON(t), c.want)
		})
	}
}

func TestAKnobCannotSetReceivedAt(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, `{"received_at":"2000-01-01T00:00:00Z","aqi":2}`)
	if got, want := k.stored().ReceivedAt, k.clk.Now().Truncate(time.Second); !got.Equal(want) {
		t.Fatalf("received_at = %v, want %v", got, want)
	}
}

func TestDeviceCheckinKeepsRoomValuesAtTheirLimits(t *testing.T) {
	cases := []string{
		`{"t_c":-40,"rh":0,"eco2":400,"tvoc":0,"aqi":1,"lux":0}`,
		`{"t_c":85,"rh":100,"eco2":65000,"tvoc":65000,"aqi":5,"lux":125000}`,
		`{"air":"normal"}`, `{"air":"warm_up"}`, `{"air":"start_up"}`, `{"air":"invalid"}`,
	}
	for _, room := range cases {
		k := newRoomKnob(t, "")
		k.checkinRoom(t, room)
		jsonEqual(t, k.roomJSON(t), room)
	}
}

func TestANonObjectRoomKeepsTheCheckinAndTheLastBlock(t *testing.T) {
	for _, room := range []string{`"warm"`, `5`, `[]`, `true`} {
		t.Run(room, func(t *testing.T) {
			k := newRoomKnob(t, "")
			k.checkinRoom(t, `{"lux":100}`)
			k.clk.advance(time.Second)
			k.checkin(t, `{"fw":"0.10.1","room":`+room+`}`)
			if got := k.app.devices.List()[0].LastCheckin.FW; got != "0.10.1" {
				t.Fatalf("checkin not stored: fw = %q", got)
			}
			jsonEqual(t, k.roomJSON(t), `{"lux":100}`)
		})
	}
}

func TestDeviceCheckinLogsDroppedRoomValuesOnlyWhenTheReasonChanges(t *testing.T) {
	k := newRoomKnob(t, "")
	var logs bytes.Buffer
	k.app.logger = slog.New(slog.NewTextHandler(&logs, nil))
	dropped := func() int { return strings.Count(logs.String(), "device room values dropped") }
	for range 3 {
		k.checkinRoom(t, `{"lux":-1}`)
	}
	if got := dropped(); got != 1 {
		t.Fatalf("Info logs for one repeated reason = %d, want 1:\n%s", got, logs.String())
	}
	k.checkinRoom(t, `{"lux":-1,"aqi":9}`)
	if got := dropped(); got != 2 {
		t.Fatalf("Info logs after the reason changed = %d, want 2", got)
	}
	k.checkinRoom(t, `{"lux":5}`)
	k.checkinRoom(t, `{"lux":-1,"aqi":9}`)
	if got := dropped(); got != 3 {
		t.Fatalf("Info logs after a clean block in between = %d, want 3", got)
	}
}

func TestDeletingAKnobForgetsItsRoomDropLog(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, `{"lux":-1}`)
	resp, b := devReq(t, k.srv, "DELETE", "/v1/devices/"+k.id, testToken, "")
	mustOK(t, "delete", resp, b)
	if k.app.roomDrops.changed(k.id, "") {
		t.Fatal("drop reason outlived the device")
	}
}

func TestDeviceRecordCloneDeepCopiesTheRoom(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, fullRoom)
	rec := devices.Record{LastCheckin: &devices.Checkin{Room: k.stored()}}
	c := rec.Clone()
	assertNoSharedPointers(t, reflect.ValueOf(*rec.LastCheckin.Room), reflect.ValueOf(*c.LastCheckin.Room))
	*c.LastCheckin.Room.AgeS.Climate = 99
	*c.LastCheckin.Room.TempC = 99
	if *rec.LastCheckin.Room.AgeS.Climate == 99 || *rec.LastCheckin.Room.TempC == 99 {
		t.Fatal("a write to the clone reached the record")
	}
}

func assertNoSharedPointers(t *testing.T, a, b reflect.Value) {
	t.Helper()
	for i := range a.NumField() {
		af, bf := a.Field(i), b.Field(i)
		if af.Kind() != reflect.Pointer {
			continue
		}
		name := a.Type().Field(i).Name
		if af.IsNil() {
			t.Fatalf("%s is nil: fill it so the test covers it", name)
		}
		if af.Pointer() == bf.Pointer() {
			t.Errorf("%s shares its pointer with the clone", name)
		}
		if af.Elem().Kind() == reflect.Struct {
			assertNoSharedPointers(t, af.Elem(), bf.Elem())
		}
	}
}

func TestDeviceListCarriesTheRoomBlock(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, `{"lux":312}`)
	resp, b := devReq(t, k.srv, "GET", "/v1/devices", testToken, "")
	var out struct {
		Devices []struct {
			LastCheckin struct {
				Room json.RawMessage `json:"room"`
			} `json:"last_checkin"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(mustOK(t, "list", resp, b), &out); err != nil {
		t.Fatal(err)
	}
	want := `{"lux":312,"received_at":"2026-10-30T12:00:00Z"}`
	if got := string(out.Devices[0].LastCheckin.Room); got != want {
		t.Fatalf("room = %s, want %s", got, want)
	}
}

func TestRoomRouteAnswersTheBlockAndItsAge(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, `{"lux":312,"age_s":{"light":3}}`)
	k.clk.advance(7*time.Second + 900*time.Millisecond)
	resp, b := devReq(t, k.srv, "GET", "/v1/devices/"+k.id+"/room", testToken, "")
	want := `{"room":{"lux":312,"age_s":{"light":3},"received_at":"2026-10-30T12:00:00Z"},"received_age_s":7}`
	if got := strings.TrimSpace(string(mustOK(t, "room", resp, b))); got != want {
		t.Fatalf("reply = %s, want %s", got, want)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestRoomRouteNeverAnswersANegativeAge(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, `{"lux":312}`)
	k.clk.advance(-time.Minute)
	resp, b := devReq(t, k.srv, "GET", "/v1/devices/"+k.id+"/room", testToken, "")
	if s := string(mustOK(t, "room", resp, b)); !strings.Contains(s, `"received_age_s":0`) {
		t.Fatalf("reply = %s", s)
	}
}

func TestRoomRouteIs404WithoutABlock(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkin(t, `{"fw":"0.9.13"}`)
	cases := map[string]string{"knob without a block": k.id, "unknown device": "knob-000000"}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			resp, b := devReq(t, k.srv, "GET", "/v1/devices/"+id+"/room", testToken, "")
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d: %s", resp.StatusCode, b)
			}
		})
	}
}

func TestRoomRouteTakesTheReadScope(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkinRoom(t, `{"lux":312}`)
	path := "/v1/devices/" + k.id + "/room"
	if resp, b := devReq(t, k.srv, "GET", path, mintClient(t, k.srv, "reader", scopeRead).Token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("read scope = %d: %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, k.srv, "GET", path, mintClient(t, k.srv, "ingest", scopeIngest).Token, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ingest scope = %d: %s", resp.StatusCode, b)
	}
	if resp, b := devReq(t, k.srv, "GET", path, k.tok, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("device token = %d: %s", resp.StatusCode, b)
	}
}

func TestRoomCapTokenIsKeptAndShown(t *testing.T) {
	k := newRoomKnob(t, "")
	k.checkin(t, `{"fw":"0.10.0","caps":{"view":[1,1],"pages":["bot"],"features":["`+featureRoom+`"]}}`)
	resp, b := devReq(t, k.srv, "GET", "/v1/devices", testToken, "")
	if s := string(mustOK(t, "list", resp, b)); !strings.Contains(s, `"features":["room"]`) {
		t.Fatalf("list = %s", s)
	}
}
