# @name    zzz-ember-weather
# @desc    Experiment: Ember's weather tile rendered on-device instead of pushed
# @author  ember
# @version 1.0
# @config  lat   text   "Latitude"  default="52.52"
# @config  lon   text   "Longitude" default="13.40"
# @config  every number "Refresh"   default=5 min=1 max=60 unit=min

# Not provisioned by Ember and not a feature — see README.md. It answers one
# question: can a 32x8 tile Ember renders server-side be produced on the device
# instead? The layout mirrors internal/render's weather tile: a drawn condition
# glyph in the left 8 columns, the temperature centred in the rest, and a
# 24-hour trend strip along the bottom two rows.
#
# It fetches Open-Meteo directly rather than Ember, because Ember's only public
# weather endpoint (GET /v1/weather/preview) serves a rendered pixel frame —
# ~7.8 KB of "#rrggbb" strings and no scalars. See README.md.

class ZzzEmberWeather
  var url, hurl
  var temp, code, label
  var trend, tmin, tmax   # up to 16 hourly temps + their range
  var ticks, phase, in_flight

  def init()
    var q = "&latitude=" + store.get("lat") + "&longitude=" + store.get("lon")
    self.url = "https://api.open-meteo.com/v1/forecast?current_weather=true" + q
    self.hurl = "https://api.open-meteo.com/v1/forecast?forecast_days=1&hourly=temperature_2m" + q
    self.temp = store.get("temp")
    self.code = store.get("code")
    self.label = self.temp == nil ? nil : str(self.temp) + "°"
    self.trend = []
    self.tmin = 0
    self.tmax = 0
    self.ticks = 0
    self.phase = 0
    self.in_flight = false
  end

  def on_body(body, status)
    self.in_flight = false
    if body == nil return end

    if self.phase == 0
      # The needle is the object, not the field: Open-Meteo emits
      # "current_weather_units":{…,"temperature":"°C",…} BEFORE the real
      # reading, so a find on "temperature": lands on the units block and the
      # window holds no number at all. (The API reference's worked example has
      # exactly this bug.) One 128-byte window then holds both values.
      var m = re.search("\"temperature\":([-0-9.]+)", body)
      if m == nil
        log("zzz-weather: no temperature in window")
        return
      end
      var t = num(m[1])
      if t == nil return end
      self.temp = t
      var half = t >= 0 ? 0.5 : -0.5
      self.label = str(int(t + half)) + "°"
      var c = re.search("\"weathercode\":(\\d+)", body)
      if c != nil self.code = num(c[1]) end
      store.set("temp", t)
      store.set("code", self.code)
      self.phase = 1        # trend on the next tick
      self.ticks = 0
      return
    end

    # matchall's first hit is the "2" of temperature_2m in the needle itself.
    var ns = re.matchall("[-0-9.]+", body)
    if ns == nil || size(ns) < 3 return end
    var n = size(ns) - 1
    var step = n / 16
    if step < 1 step = 1 end
    self.trend = []
    var i = 1
    while i <= n && size(self.trend) < 16
      var v = num(ns[i])
      if v != nil
        self.trend.push(v)
        if size(self.trend) == 1
          self.tmin = v
          self.tmax = v
        end
        self.tmin = min(self.tmin, v)
        self.tmax = max(self.tmax, v)
      end
      i += step
    end
    self.phase = 0
  end

  def loop()
    if self.ticks <= 0
      self.ticks = 60 * store.get("every")
      if !self.in_flight
        self.in_flight = true
        if self.phase == 0
          http.get(self.url, / b, st -> self.on_body(b, st),
                   {'find': "\"current_weather\":{", 'keep': 128})
        else
          http.get(self.hurl, / b, st -> self.on_body(b, st),
                   {'find': "\"temperature_2m\":[", 'keep': 192})
        end
      end
    end
    self.ticks -= 1
  end

  def draw()
    clear()

    # Drawn glyph, not a gallery icon: nothing to install, nothing to fail.
    var c = self.code
    if c == nil || c <= 1
      circle_fill(3, 3, 2, 0xFFC14D)
    elif c < 45
      circle_fill(2, 2, 2, 0x8A6A2A)
      rect_fill(1, 4, 6, 2, 0x99AABB)
    else
      rect_fill(1, 1, 6, 3, 0x8899AA)
      line(2, 5, 2, 6, 0x3399FF)
      line(4, 5, 4, 6, 0x3399FF)
      line(6, 5, 6, 6, 0x3399FF)
    end

    var n = size(self.trend)
    if n > 1 && self.tmax > self.tmin
      var i = 0
      while i < n
        var x = 9 + (i * 23) / n
        var y = 7
        if (self.trend[i] - self.tmin) * 2 >= self.tmax - self.tmin
          y = 6
        end
        pixel(x, y, 0x334455)
        i += 1
      end
    end

    if self.label == nil
      text(10, 5, "...", 0x666666)
      return
    end
    var col = 0x00FF00
    if self.temp >= 28
      col = 0xFF4000
    elif self.temp <= 0
      col = 0x00AAFF
    end
    text(9 + (23 - text_ink_width(self.label)) / 2, 5, self.label, col)
  end
end

return ZzzEmberWeather()
