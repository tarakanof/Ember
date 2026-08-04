package awtrix

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recorded captures the one request a test server saw.
type recorded struct {
	method      string
	path        string
	rawQuery    string
	contentType string
	body        []byte
}

// serve starts a test server that records the request and replies with status
// and body. Returns the client pointed at it and the recording.
func serve(t *testing.T, status int, respBody string) (*Client, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.rawQuery = r.URL.RawQuery
		rec.contentType = r.Header.Get("Content-Type")
		rec.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, 2*time.Second), rec
}

func decodeBody(t *testing.T, rec *recorded) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.body, &m); err != nil {
		t.Fatalf("request body is not JSON: %v (%q)", err, rec.body)
	}
	return m
}

func TestPushApp(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	err := c.PushApp(context.Background(), "ember weather", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("PushApp: %v", err)
	}
	if rec.method != http.MethodPut || rec.path != "/api/v1/apps/pushed/ember weather" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if rec.contentType != "application/json" {
		t.Fatalf("Content-Type = %q", rec.contentType)
	}
	if m := decodeBody(t, rec); m["text"] != "hi" {
		t.Fatalf("body = %v", m)
	}
}

func TestDeleteApp(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.DeleteApp(context.Background(), "ember-weather"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/apps/ember-weather" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if len(rec.body) != 0 {
		t.Fatalf("DELETE must send no body, got %q", rec.body)
	}
}

func TestListApps(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `[
		{"name":"Time","enabled":true,"inLoop":true,"slot":null,"present":true,"origin":"builtin"},
		{"name":"ember","enabled":true,"inLoop":true,"slot":null,"present":true,"origin":"pushed"}
	]`)
	apps, err := c.ListApps(context.Background())
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/apps" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if len(apps) != 2 || apps[0].Name != "Time" || apps[0].Origin != "builtin" ||
		apps[1].Name != "ember" || apps[1].Origin != "pushed" {
		t.Fatalf("apps = %+v", apps)
	}
}

func TestNotify(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.Notify(context.Background(), map[string]any{"text": "yo"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/notifications" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

func TestDismissNotify(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.DismissNotify(context.Background()); err != nil {
		t.Fatalf("DismissNotify: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/notifications/active" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

func TestPlayRTTTL(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.PlayRTTTL(context.Background(), "beep:d=16,o=6,b=140:c"); err != nil {
		t.Fatalf("PlayRTTTL: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/sounds/play" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if m := decodeBody(t, rec); m["rtttl"] != "beep:d=16,o=6,b=140:c" {
		t.Fatalf("body = %v", m)
	}
}

func TestPlaySound(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.PlaySound(context.Background(), "alarm"); err != nil {
		t.Fatalf("PlaySound: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/sounds/play" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if m := decodeBody(t, rec); m["name"] != "alarm" {
		t.Fatalf("body = %v", m)
	}
}

func TestSetIndicator(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.SetIndicator(context.Background(), 2, map[string]any{"color": "#FF0000"}); err != nil {
		t.Fatalf("SetIndicator: %v", err)
	}
	if rec.method != http.MethodPut || rec.path != "/api/v1/indicators/2" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

func TestClearIndicator(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.ClearIndicator(context.Background(), 3); err != nil {
		t.Fatalf("ClearIndicator: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/indicators/3" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

func TestIndicatorIndexValidated(t *testing.T) {
	c, _ := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.SetIndicator(context.Background(), 0, map[string]any{"color": "#FF0000"}); err == nil {
		t.Fatal("index 0 must be rejected client-side")
	}
	if err := c.ClearIndicator(context.Background(), 4); err == nil {
		t.Fatal("index 4 must be rejected client-side")
	}
}

func TestPatchSettings(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"brightness":120}`)
	if err := c.PatchSettings(context.Background(), map[string]any{"brightness": 120}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	if rec.method != http.MethodPatch || rec.path != "/api/v1/settings" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if rec.contentType != "application/json" {
		t.Fatalf("Content-Type = %q", rec.contentType)
	}
}

func TestSwitchApp(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.SwitchApp(context.Background(), "ember"); err != nil {
		t.Fatalf("SwitchApp: %v", err)
	}
	if rec.method != http.MethodPut || rec.path != "/api/v1/apps/active" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if m := decodeBody(t, rec); m["name"] != "ember" {
		t.Fatalf("body = %v", m)
	}
}

func TestListIcons(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"files":[{"name":"29802.gif","size":1234},{"name":"6396.gif","size":99}],"usedBytes":1,"totalBytes":2}`)
	names, err := c.ListIcons(context.Background())
	if err != nil {
		t.Fatalf("ListIcons: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/files" || rec.rawQuery != "dir=%2FICONS" {
		t.Fatalf("got %s %s?%s", rec.method, rec.path, rec.rawQuery)
	}
	if len(names) != 2 || names[0] != "29802.gif" || names[1] != "6396.gif" {
		t.Fatalf("names = %v", names)
	}
}

func TestPutIcon(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.PutIcon(context.Background(), "29802.gif", []byte("GIF89a...")); err != nil {
		t.Fatalf("PutIcon: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/files" || rec.rawQuery != "dir=%2FICONS" {
		t.Fatalf("got %s %s?%s", rec.method, rec.path, rec.rawQuery)
	}
	mt, params, err := mime.ParseMediaType(rec.contentType)
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("Content-Type = %q (%v)", rec.contentType, err)
	}
	mr := multipart.NewReader(strings.NewReader(string(rec.body)), params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("no multipart part: %v", err)
	}
	if part.FileName() != "29802.gif" {
		t.Fatalf("part filename = %q", part.FileName())
	}
	data, _ := io.ReadAll(part)
	if string(data) != "GIF89a..." {
		t.Fatalf("part data = %q", data)
	}
}

func TestDeviceInfo(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"version":"1.0.13","uid":"e868e705ffb8","boardType":"awtrixng","uptimeSeconds":42}`)
	info, err := c.DeviceInfo(context.Background())
	if err != nil {
		t.Fatalf("DeviceInfo: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/device" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if info.UID != "e868e705ffb8" || info.BoardType != "awtrixng" || info.Version != "1.0.13" || info.UptimeSeconds != 42 {
		t.Fatalf("info = %+v", info)
	}
}

func TestReboot(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.Reboot(context.Background()); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/device/reboot" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

// The NG error envelope ({"error":{code,message,field}}) must surface as a
// typed *APIError so callers can log the offending field on 422s.
func TestErrorEnvelopeDecoding(t *testing.T) {
	c, _ := serve(t, http.StatusUnprocessableEntity,
		`{"error":{"code":"validationFailed","message":"unknown key \"noScroll\"","field":"noScroll"}}`)
	err := c.PushApp(context.Background(), "x", map[string]any{"noScroll": true})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 422 || apiErr.Code != "validationFailed" || apiErr.Field != "noScroll" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	for _, want := range []string{"422", "validationFailed", "noScroll"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error text %q missing %q", err.Error(), want)
		}
	}
}

func TestErrorWithoutEnvelope(t *testing.T) {
	c, _ := serve(t, http.StatusInternalServerError, `boom`)
	err := c.Notify(context.Background(), map[string]any{"text": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 500 || !strings.Contains(apiErr.Message, "boom") {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestBaseURLRequired(t *testing.T) {
	c := NewClient("", time.Second)
	if err := c.Notify(context.Background(), map[string]any{}); err == nil {
		t.Fatal("empty base URL must error")
	}
}

func TestDismissNotifyByName(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.DismissNotifyByName(context.Background(), "ember reminder"); err != nil {
		t.Fatalf("DismissNotifyByName: %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/api/v1/notifications/ember reminder" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
}

func TestDismissNotifyByNameRejectsEmptyName(t *testing.T) {
	c, _ := serve(t, http.StatusOK, `{"ok":true}`)
	if err := c.DismissNotifyByName(context.Background(), ""); err == nil {
		t.Fatal("empty name must be rejected client-side (it would hit the collection route)")
	}
}

func TestCapabilities(t *testing.T) {
	body := `{"effects":["Fade","Matrix"],"paletteEffects":["Fade"],
	  "transitions":["Slide","Dim","Zoom"],"overlays":["rain"],
	  "palettes":["Ocean","Lava"],"radio":false,"gpio":{"soc":"esp32","max":39}}`
	c, rec := serve(t, http.StatusOK, body)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if rec.method != http.MethodGet || rec.path != "/api/v1/capabilities" {
		t.Fatalf("got %s %s", rec.method, rec.path)
	}
	if len(caps.Effects) != 2 || len(caps.PaletteEffects) != 1 || len(caps.Transitions) != 3 ||
		len(caps.Overlays) != 1 || len(caps.Palettes) != 2 || caps.Radio {
		t.Fatalf("decoded = %+v", caps)
	}
	// gpio round-trips verbatim so a re-marshal reproduces the NG shape.
	if !strings.Contains(string(caps.GPIO), `"soc":"esp32"`) {
		t.Fatalf("gpio = %s", caps.GPIO)
	}
	out, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-marshal not JSON: %v", err)
	}
	for _, k := range []string{"effects", "paletteEffects", "transitions", "overlays", "palettes", "radio", "gpio"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("re-marshalled shape missing %q: %s", k, out)
		}
	}
}

func TestCapabilitiesPropagatesAPIError(t *testing.T) {
	c, _ := serve(t, http.StatusNotFound, `{"error":{"code":"notFound","message":"not found"}}`)
	if _, err := c.Capabilities(context.Background()); err == nil {
		t.Fatal("want error on 404")
	}
}

func TestTrailingSlashTrimmed(t *testing.T) {
	c, rec := serve(t, http.StatusOK, `{"ok":true}`)
	c2 := NewClient(c.BaseURL()+"/", 2*time.Second)
	if err := c2.Notify(context.Background(), map[string]any{"text": "x"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if rec.path != "/api/v1/notifications" {
		t.Fatalf("path = %q (double slash?)", rec.path)
	}
}
