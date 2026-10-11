package main

import (
	"math"
	"time"
)

type knobWeatherDetail struct {
	UTCOffset int64         `json:"utc_offset"`
	Now       *knobWxNow    `json:"now,omitempty"`
	Today     *knobWxToday  `json:"today,omitempty"`
	Rain      *knobWxRain   `json:"rain,omitempty"`
	Days      []knobWxDay   `json:"days,omitempty"`
	Twilight  *knobTwilight `json:"twilight,omitempty"`
	Moon      knobMoon      `json:"moon"`
	AQI       *int          `json:"aqi,omitempty"`
}

type knobWxNow struct {
	Cloud   *int `json:"cloud,omitempty"`
	UV      *int `json:"uv,omitempty"`
	WindKmh *int `json:"wind_kmh,omitempty"`
	GustKmh *int `json:"gust_kmh,omitempty"`
	WindDir *int `json:"wind_dir,omitempty"`
	Rain    *int `json:"rain,omitempty"`
}

type knobWxToday struct {
	MinC       *int `json:"min_c,omitempty"`
	MaxC       *int `json:"max_c,omitempty"`
	UVMax      *int `json:"uv_max,omitempty"`
	GustMaxKmh *int `json:"gust_max_kmh,omitempty"`
	Pop        *int `json:"pop,omitempty"`
}

type knobWxRain struct {
	Until  *int64     `json:"until,omitempty"`
	Spells [][4]int64 `json:"spells"`
}

type knobWxDay struct {
	T          int64  `json:"t"`
	End        int64  `json:"end"`
	Cond       string `json:"cond,omitempty"`
	Code       string `json:"code,omitempty"`
	MinC       *int   `json:"min_c,omitempty"`
	MaxC       *int   `json:"max_c,omitempty"`
	Pop        *int   `json:"pop,omitempty"`
	PrecipMM10 *int   `json:"precip_mm10,omitempty"`
}

type knobTwilight struct {
	Dawn        *int64 `json:"dawn,omitempty"`
	BlueEnd     *int64 `json:"blue_end,omitempty"`
	GoldenEnd   *int64 `json:"golden_end,omitempty"`
	GoldenStart *int64 `json:"golden_start,omitempty"`
	BlueStart   *int64 `json:"blue_start,omitempty"`
	Dusk        *int64 `json:"dusk,omitempty"`
}

type knobMoon struct {
	Phase int    `json:"phase"`
	Illum int    `json:"illum"`
	Up    *bool  `json:"up,omitempty"`
	Rise  *int64 `json:"rise,omitempty"`
	Set   *int64 `json:"set,omitempty"`
}

func roundedInt(v *float64, scale float64) *int {
	if v == nil {
		return nil
	}
	return new(int(math.Round(*v * scale)))
}

func unixOpt(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	return new(t.Unix())
}

func newKnobWeatherDetail(wx weatherState, cfg WeatherConfig) *knobWeatherDetail {
	f := wx.faces()
	n := wx.Obs.Now
	d := &knobWeatherDetail{UTCOffset: int64(f.UTCOffset / time.Second)}
	now := knobWxNow{Cloud: roundedInt(n.Cloud, 1), UV: roundedInt(n.UV, 1), WindKmh: roundedInt(n.WindKmh, 1),
		GustKmh: roundedInt(n.GustKmh, 1), WindDir: roundedInt(n.WindDir, 1)}
	if now.WindDir != nil {
		*now.WindDir %= 360
	}
	if f.HaveRainNow {
		now.Rain = new(f.RainNow)
	}
	if now != (knobWxNow{}) {
		d.Now = &now
	}
	if t := f.Today; t != nil {
		d.Today = &knobWxToday{MinC: roundedInt(t.MinC, 1), MaxC: roundedInt(t.MaxC, 1), UVMax: roundedInt(t.UVMax, 1),
			GustMaxKmh: roundedInt(t.GustMaxKmh, 1), Pop: roundedInt(t.Pop, 1)}
	}
	if f.HaveRainNow {
		d.Rain = &knobWxRain{Until: unixOpt(f.RainUntil), Spells: make([][4]int64, 0, len(f.Spells))}
		for _, s := range f.Spells {
			kind := int64(0)
			if s.Snow {
				kind = 1
			}
			d.Rain.Spells = append(d.Rain.Spells, [4]int64{s.Start.Unix(), s.End.Unix(), int64(s.Intensity), kind})
		}
	}
	for _, day := range f.Days {
		d.Days = append(d.Days, knobWxDay{T: day.Start.Unix(), End: dayEnd(day).Unix(), Cond: day.Cond, Code: day.Code,
			MinC: roundedInt(day.MinC, 1), MaxC: roundedInt(day.MaxC, 1), Pop: roundedInt(day.Pop, 1), PrecipMM10: roundedInt(day.PrecipMM, 10)})
	}
	if f.HaveAstro {
		tw := f.Twilight
		r := func(t time.Time) *int64 { return unixOpt(t.Round(sunRounding)) }
		k := knobTwilight{Dawn: r(tw.Dawn), BlueEnd: r(tw.BlueEnd), GoldenEnd: r(tw.GoldenEnd),
			GoldenStart: r(tw.GoldenStart), BlueStart: r(tw.BlueStart), Dusk: r(tw.Dusk)}
		if k != (knobTwilight{}) {
			d.Twilight = &k
		}
	}
	d.Moon = knobMoon{Phase: int(math.Round(f.MoonPhase*100)) % 100, Illum: int(math.Round(f.MoonIllum * 100)),
		Rise: unixOpt(f.MoonRise), Set: unixOpt(f.MoonSet)}
	if f.HaveAstro {
		d.Moon.Up = new(f.MoonUp)
	}
	if cfg.AirTileEnabled() && wx.airLive() {
		d.AQI = new(int(math.Round(wx.Air.AQI)))
	}
	return d
}
