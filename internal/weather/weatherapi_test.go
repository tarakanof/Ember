package weather

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarakanof/ember/internal/render"
)

const testWeatherAPIKey = "wa-SECRET+key/7"

var waT0 = time.Date(2026, 10, 11, 8, 15, 0, 0, time.UTC)

func waFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../cmd/ember/testdata/weather/weatherapi_forecast.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type waStub struct {
	srv    *httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	retry  atomic.Pointer[string]
	body   atomic.Pointer[[]byte]
	query  atomic.Pointer[string]
	path   atomic.Pointer[string]
}

func newWAStub(t *testing.T, body []byte) *waStub {
	t.Helper()
	st := &waStub{}
	st.body.Store(&body)
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.hits.Add(1)
		q, p := r.URL.RawQuery, r.URL.Path
		st.query.Store(&q)
		st.path.Store(&p)
		if code := int(st.status.Load()); code != 0 {
			if v := st.retry.Load(); v != nil {
				w.Header().Set("Retry-After", *v)
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"code":2007,"message":"quota exceeded for key ` + r.URL.Query().Get("key") + `"}}`))
			return
		}
		_, _ = w.Write(*st.body.Load())
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func newWAFetcher(base string) *Fetcher {
	wf := NewFetcher()
	wf.WeatherAPIBase = base
	wf.WeatherAPIKey = testWeatherAPIKey
	return wf
}

var waBelgrade = Config{Provider: WeatherAPI, Latitude: 44.8176, Longitude: 20.4633}

func TestFetchWeatherAPISendsKeyLocationAndDays(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	if _, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{}); err != nil {
		t.Fatal(err)
	}
	if p := *st.path.Load(); p != "/v1/forecast.json" {
		t.Errorf("path = %s", p)
	}
	q, err := url.ParseQuery(*st.query.Load())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"key": testWeatherAPIKey, "q": "44.8176,20.4633", "days": "3", "aqi": "no", "alerts": "no"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
}

func TestFetchWeatherAPIMapsCurrentHourlyAndDays(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	obs, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if !obs.ObservedAt.Equal(waT0) || obs.TempC != 14.2 || obs.Condition != render.WeatherClouds || obs.ConditionCode != "2" {
		t.Errorf("current = %v %v %q %q, want last_updated 08:15Z, 14.2 °C, partly cloudy as WMO 2", obs.ObservedAt, obs.TempC, obs.Condition, obs.ConditionCode)
	}
	n := obs.Now
	if !approx(n.Cloud, 63) || !approx(n.UV, 2) || !approx(n.WindKmh, 18) || !approx(n.GustKmh, 36) || !approx(n.WindDir, 225) {
		t.Errorf("now = %+v, want km/h as sent", n)
	}
	if !obs.TZKnown || obs.TZOffsetSeconds != 7200 {
		t.Errorf("tz = (%v, %d), want Europe/Belgrade's +7200 from tz_id", obs.TZKnown, obs.TZOffsetSeconds)
	}
	hourStart := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	if !obs.HourlyStart.Equal(hourStart) || len(obs.Hourly) != forecastFetchHours || obs.Hourly[0] != 14 || obs.Hourly[2] != 1.5 {
		t.Errorf("hourly start %v, %d values %v, want 24 from the hour holding now", obs.HourlyStart, len(obs.Hourly), obs.Hourly)
	}
	if len(obs.Steps) != forecastFetchHours || !obs.Steps[0].At.Equal(waT0) || !obs.Steps[1].At.Equal(hourStart.Add(time.Hour)) {
		t.Fatalf("steps = %d, first at %v, second at %v, want current then the hours after it", len(obs.Steps), obs.Steps[0].At, obs.Steps[1].At)
	}
	if s := obs.Steps[8]; s.Condition != render.WeatherStorm || s.Code != "95" || !s.Severe {
		t.Errorf("16:00Z step = %+v, want thunder as WMO 95", s)
	}

	if len(obs.Days) != 3 {
		t.Fatalf("days = %+v, want 3 (free plan)", obs.Days)
	}
	d0 := obs.Days[0]
	if !d0.Start.Equal(time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC)) || d0.Code != "95" || !approx(d0.MinC, 9.1) || !approx(d0.MaxC, 18.5) ||
		!approx(d0.Pop, 80) || !approx(d0.PrecipMM, 7.3) || !approx(d0.UVMax, 4) || !approx(d0.GustMaxKmh, 34) {
		t.Errorf("today = %+v, want local midnight, thunder outweighing the rest from now on, pop = max(rain, snow), gust = max hourly", d0)
	}
	if d1 := obs.Days[1]; d1.Code != "0" || d1.Cond != render.WeatherClear || !approx(d1.Pop, 0) {
		t.Errorf("day 1 = %+v, want clear: 22 h clear beat 2 h fog though the day's own condition says fog", d1)
	}
	if d2 := obs.Days[2]; d2.Code != "65" || !approx(d2.PrecipMM, 30) {
		t.Errorf("day 2 = %+v, want 6 h heavy rain over 18 h cloudy", d2)
	}
}

func TestFetchWeatherAPISplitsHourlyRainIntoQuarterHours(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	obs, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if obs.PrecipStep != 15*time.Minute || len(obs.Precip) != 48 {
		t.Fatalf("step %v, %d steps, want 15 min and 48", obs.PrecipStep, len(obs.Precip))
	}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 11, h, m, 0, 0, time.UTC) }
	want := map[int]PrecipStep{
		0:  {At: at(8, 0), MMh: 0.8},
		3:  {At: at(8, 45), MMh: 0.8},
		4:  {At: at(9, 0), MMh: 2},
		8:  {At: at(10, 0), MMh: 0.5, Snow: true},
		12: {At: at(11, 0)},
		32: {At: at(16, 0), MMh: 4},
		47: {At: at(19, 45)},
	}
	for i, w := range want {
		if g := obs.Precip[i]; !g.At.Equal(w.At) || !approxEq(g.MMh, w.MMh) || g.Snow != w.Snow {
			t.Errorf("step %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestWeatherAPIConditionCodesAllMapToWMO(t *testing.T) {
	documented := []int{1000, 1003, 1006, 1009, 1012, 1015, 1018, 1021, 1024, 1027, 1030, 1033, 1036, 1039, 1042, 1045, 1048,
		1063, 1066, 1069, 1072, 1087, 1114, 1117, 1135, 1147, 1150, 1153, 1168, 1171, 1180, 1183, 1186, 1189, 1192, 1195,
		1198, 1201, 1204, 1207, 1210, 1213, 1216, 1219, 1222, 1225, 1237, 1240, 1243, 1246, 1249, 1252, 1255, 1258, 1261,
		1264, 1273, 1276, 1279, 1282}
	for _, c := range documented {
		wmo, ok := weatherAPIWMO(c)
		if !ok {
			t.Errorf("code %d has no WMO equivalent", c)
			continue
		}
		if !slices.Contains(wmoSeverity, wmo) {
			t.Errorf("code %d → WMO %d has no severity", c, wmo)
		}
	}
	cases := map[int]string{1000: render.WeatherClear, 1009: render.WeatherClouds, 1135: render.WeatherFog,
		1183: render.WeatherRain, 1201: render.WeatherRain, 1225: render.WeatherSnow, 1237: render.WeatherSnow, 1276: render.WeatherStorm}
	for code, want := range cases {
		wmo, _ := weatherAPIWMO(code)
		if got, _ := wmoCondition(wmo); got != want {
			t.Errorf("code %d → WMO %d → %q, want %q", code, wmo, got, want)
		}
	}
	if _, ok := weatherAPIWMO(999); ok {
		t.Error("unknown code mapped")
	}
}

func TestFetchWeatherAPILeavesMissingValuesOut(t *testing.T) {
	body := `{"location":{"tz_id":"Europe/Belgrade"},"current":{"last_updated_epoch":1791706500,"temp_c":7,"condition":{"code":1}},
"forecast":{"forecastday":[{"date":"2026-10-11","day":{"maxtemp_c":9},"hour":[]}]}}`
	st := newWAStub(t, []byte(body))
	obs, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	if n := obs.Now; n.Cloud != nil || n.UV != nil || n.WindKmh != nil || n.GustKmh != nil || n.WindDir != nil {
		t.Errorf("now = %+v, want every value nil", n)
	}
	if obs.Condition != render.WeatherClouds || obs.ConditionCode != "" {
		t.Errorf("unknown code = %q %q, want clouds without a code", obs.Condition, obs.ConditionCode)
	}
	if len(obs.Precip) != 0 || len(obs.Hourly) != 0 {
		t.Errorf("precip %+v hourly %v, want none", obs.Precip, obs.Hourly)
	}
	if len(obs.Days) != 1 || !approx(obs.Days[0].MaxC, 9) || obs.Days[0].MinC != nil || obs.Days[0].Pop != nil || obs.Days[0].PrecipMM != nil || obs.Days[0].Code != "" {
		t.Errorf("days = %+v, want one day with only max", obs.Days)
	}
}

func TestFetchWeatherAPIWithoutATimeZoneUsesTheHint(t *testing.T) {
	for _, tz := range []string{"", "Not/AZone", "../../etc/passwd", "Local"} {
		body := fmt.Sprintf(`{"location":{"tz_id":%q},"current":{"last_updated_epoch":1791706500,"temp_c":7}}`, tz)
		st := newWAStub(t, []byte(body))
		obs, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{TZOffsetSeconds: 3600, TZKnown: true})
		if err != nil {
			t.Fatal(err)
		}
		if !obs.TZKnown || obs.TZOffsetSeconds != 3600 {
			t.Errorf("tz_id %q: tz = (%v, %d), want the hint", tz, obs.TZKnown, obs.TZOffsetSeconds)
		}
	}
}

func TestFetchWeatherAPIRejectsAnEmptyReply(t *testing.T) {
	st := newWAStub(t, []byte(`{"location":{}}`))
	if _, err := newWAFetcher(st.srv.URL).fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{}); err == nil || err.Error() != "weatherapi: invalid response" {
		t.Fatalf("err = %v, want fixed text", err)
	}
}

func TestFetchWeatherAPIWithoutKeyMakesNoRequest(t *testing.T) {
	st := newWAStub(t, waFixture(t))
	wf := newWAFetcher(st.srv.URL)
	wf.WeatherAPIKey = ""
	_, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	if !errors.Is(err, errWeatherAPINoKey) || st.hits.Load() != 0 {
		t.Errorf("err = %v, %d requests, want errWeatherAPINoKey and none", err, st.hits.Load())
	}
}

func TestFetchWeatherAPIErrorsAreFixedText(t *testing.T) {
	st := newWAStub(t, nil)
	st.status.Store(http.StatusForbidden)
	wf := newWAFetcher(st.srv.URL)
	_, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	if err == nil || err.Error() != "weather http 403" {
		t.Errorf("http error = %v, want the numeric status only", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	wf.WeatherAPIBase = dead.URL
	if _, err = wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{}); err == nil || err.Error() != "weatherapi: request failed" {
		t.Errorf("transport error = %v, want fixed text", err)
	}
}

func TestFetchWeatherAPINeverLeaksKeyFragments(t *testing.T) {
	fields := []string{
		`{"location":{"tz_id":%q},"current":{"last_updated_epoch":1}}`,
		`{"current":{"last_updated_epoch":%q}}`,
		`{"current":{"last_updated_epoch":1,"temp_c":%q}}`,
		`{"current":{"last_updated_epoch":1,"condition":{"code":%q}}}`,
		`{"current":{"last_updated_epoch":1},"forecast":{"forecastday":[{"date":%q,"day":{},"hour":[]}]}}`,
		`{"current":{"last_updated_epoch":1},"forecast":{"forecastday":[{"date":"2026-10-11","day":{"maxtemp_c":%q},"hour":[]}]}}`,
		`{"current":{"last_updated_epoch":1},"forecast":{"forecastday":[{"date":"2026-10-11","day":{},"hour":[{"time_epoch":%q}]}]}}`,
		`{"current":{"last_updated_epoch":1},"forecast":{"forecastday":[{"date":"2026-10-11","day":{},"hour":[{"time_epoch":1,"precip_mm":%q}]}]}}`,
		`{"forecast":%q}`,
		`%s`,
	}
	for fi, f := range fields {
		for n := 4; n <= len(straddleKey); n++ {
			st := newWAStub(t, []byte(fmt.Sprintf(f, straddleKey[:n])))
			wf := newWAFetcher(st.srv.URL)
			wf.WeatherAPIKey = straddleKey
			if _, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{}); err != nil {
				assertNoKeyFragment(t, fmt.Sprintf("field %d prefix %d", fi, n), err)
			}
		}
	}

	var body atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(*body.Load()))
	}))
	t.Cleanup(srv.Close)
	wf := newWAFetcher(srv.URL)
	wf.WeatherAPIKey = straddleKey
	for off := 0; off <= 300; off++ {
		b := strings.Repeat("x", off) + straddleKey + strings.Repeat("y", 40)
		body.Store(&b)
		_, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
		assertNoKeyFragment(t, fmt.Sprintf("body offset %d", off), err)
	}

	reason := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 403 " + straddleKey[:9] + "\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = buf.Flush()
	}))
	t.Cleanup(reason.Close)
	wf.WeatherAPIBase = reason.URL
	_, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	assertNoKeyFragment(t, "reason phrase", err)
}

func TestProvidersAvailableFollowEachKey(t *testing.T) {
	cases := []struct {
		tomorrow, wa string
		want         []string
	}{
		{"", "", []string{"open-meteo", "met-no"}},
		{"", testWeatherAPIKey, []string{"open-meteo", "met-no", WeatherAPI}},
		{testTomorrowKey, testWeatherAPIKey, Providers},
	}
	for _, c := range cases {
		wf := NewFetcher()
		wf.TomorrowKey, wf.WeatherAPIKey = c.tomorrow, c.wa
		if got := wf.ProvidersAvailable(); !slices.Equal(got, c.want) {
			t.Errorf("keys (%q, %q): %q, want %q", c.tomorrow, c.wa, got, c.want)
		}
	}
}

func TestFetchWeatherAPIWithoutACurrentTimeStartsAtNow(t *testing.T) {
	var hours []string
	for i := range 24 {
		precip := 0.0
		if i == 15 {
			precip = 3
		}
		hours = append(hours, fmt.Sprintf(`{"time_epoch":%d,"temp_c":%d,"condition":{"code":1000},"precip_mm":%v}`, 1791669600+i*3600, 10+i, precip))
	}
	body := `{"location":{"tz_id":"Europe/Belgrade"},"current":{"temp_c":30},
"forecast":{"forecastday":[{"date":"2026-10-11","day":{},"hour":[` + strings.Join(hours, ",") + `]}]}}`
	st := newWAStub(t, []byte(body))
	wf := newWAFetcher(st.srv.URL)
	now := time.Date(2026, 10, 11, 12, 20, 0, 0, time.UTC)
	wf.now = func() time.Time { return now }
	obs, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{})
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if !obs.ObservedAt.Equal(hour) || obs.TempC != 24 || !obs.HourlyStart.Equal(hour) || !obs.Precip[0].At.Equal(hour) {
		t.Errorf("observed %v at %v °C, hourly from %v, rain from %v; want the hour holding now (12:00Z), not local midnight (a current block without a time is not used)",
			obs.ObservedAt, obs.TempC, obs.HourlyStart, obs.Precip[0].At)
	}
	if g := obs.Precip[4*1]; !approxEq(g.MMh, 3) {
		t.Errorf("13:00Z step = %+v, want the 3 mm/h hour inside the 12-h window", g)
	}
}

func TestFetchWeatherAPIFailsWithoutAnyCurrentOrHourlyStep(t *testing.T) {
	for _, body := range []string{
		`{"forecast":{"forecastday":[{"date":"2026-10-11","hour":[]}]}}`,
		`{"current":{"temp_c":7},"forecast":{"forecastday":[{"date":"2026-10-11","hour":[{"time_epoch":0,"temp_c":3}]}]}}`,
		`{"forecast":{"forecastday":[{"date":"2026-10-11","hour":[{"time_epoch":1791669600,"temp_c":3}]}]}}`,
	} {
		st := newWAStub(t, []byte(body))
		wf := newWAFetcher(st.srv.URL)
		wf.now = func() time.Time { return time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC) }
		if _, err := wf.fetchWeatherAPI(t.Context(), waBelgrade, FetchHint{}); err == nil || err.Error() != "weatherapi: invalid response" {
			t.Errorf("%s: err = %v, want invalid response", body, err)
		}
	}
}

func waDSTBody(t *testing.T, dates []string, current time.Time) []byte {
	t.Helper()
	var days []string
	for _, d := range dates {
		days = append(days, fmt.Sprintf(`{"date":%q,"day":{"maxtemp_c":10},"hour":[]}`, d))
	}
	return []byte(fmt.Sprintf(`{"location":{"tz_id":"Europe/Belgrade"},"current":{"last_updated_epoch":%d,"temp_c":5},"forecast":{"forecastday":[%s]}}`,
		current.Unix(), strings.Join(days, ",")))
}

func TestAppWeatherSourcesPairEachKeyedIDWithItsKey(t *testing.T) {
	src, err := os.ReadFile("../../macos/Sources/EmberKit/Settings/WeatherSources.swift")
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{}
	for _, m := range regexp.MustCompile(`static let (\w+) = "([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		consts[m[1]] = m[2]
	}
	got := map[string]string{}
	for _, m := range regexp.MustCompile(`WeatherSourceOption\(id: ("[^"]+"|\w+),[^\n]*keyEnv: "([^"]+)"\)`).FindAllStringSubmatch(string(src), -1) {
		id := strings.Trim(m[1], `"`)
		if v, ok := consts[m[1]]; ok {
			id = v
		}
		got[id] = m[2]
	}
	want := map[string]string{}
	for p, k := range KeyedProviders {
		want[p] = k.Env
	}
	if !maps.Equal(got, want) {
		t.Errorf("app keyed providers %v, server %v", got, want)
	}
}

func TestAppWeatherSourcesNameEachServerKey(t *testing.T) {
	src, err := os.ReadFile("../../macos/Sources/EmberKit/Settings/WeatherSources.swift")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "keyEnv: \""); n != len(KeyedProviders) {
		t.Errorf("app lists %d keyed providers, server %d", n, len(KeyedProviders))
	}
	for p, k := range KeyedProviders {
		if !strings.Contains(string(src), `keyEnv: "`+k.Env+`"`) {
			t.Errorf("app has no keyEnv %q for %s", k.Env, p)
		}
	}
}
