import Testing
import Foundation
@testable import EmberKit

@Test func decodesWeatherConfigSnakeCase() throws {
    let json = #"""
    {"enabled":true,"provider":"met-no","latitude":52.37,"longitude":4.9,
     "location_name":"Amsterdam","units":"imperial","refresh_minutes":15,
     "rotate_in_apps":false,"popup_interval_minutes":60,"popup_duration_seconds":20,
     "popup_on_change":false,"severe_alert":true,"severe_sound":"alarm","use_native_icons":true}
    """#
    let c = try JSONDecoder().decode(WeatherConfig.self, from: Data(json.utf8))
    #expect(c.enabled)
    #expect(c.provider == "met-no")
    #expect(c.latitude == 52.37)
    #expect(c.locationName == "Amsterdam")
    #expect(c.units == "imperial")
    #expect(c.refreshMinutes == 15)
    #expect(c.rotateInApps == false)
    #expect(c.popupIntervalMinutes == 60)
    #expect(c.severeSound == "alarm")
    #expect(c.useNativeIcons)
}

@Test func weatherConfigEncodesServerKeys() throws {
    let c = WeatherConfig(enabled: true, latitude: 1, longitude: 2)
    let data = try JSONEncoder().encode(c)
    let s = String(decoding: data, as: UTF8.self)
    for key in ["location_name", "refresh_minutes", "rotate_in_apps",
                "forecast_tile", "forecast_hours", "sun_popups", "moon_phase",
                "popup_interval_minutes", "popup_duration_seconds",
                "popup_on_change", "severe_alert", "severe_sound", "use_native_icons"] {
        #expect(s.contains(key), "encoded weather config missing key \(key)")
    }
    let back = try JSONDecoder().decode(WeatherConfig.self, from: data)
    #expect(back == c)
}

@Test func weatherConfigForecastSkyFieldsDecodeAndDefault() throws {
    let json = #"""
    {"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2,
     "forecast_tile":false,"forecast_hours":12,"sun_popups":false,"moon_phase":false}
    """#
    let c = try JSONDecoder().decode(WeatherConfig.self, from: Data(json.utf8))
    #expect(c.forecastTile == false)
    #expect(c.forecastHours == 12)
    #expect(c.sunPopups == false)
    #expect(c.moonPhase == false)

    let old = #"{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2}"#
    let d = try JSONDecoder().decode(WeatherConfig.self, from: Data(old.utf8))
    #expect(d.forecastTile)
    #expect(d.forecastHours == 24)
    #expect(d.sunPopups)
    #expect(d.moonPhase)
}

@Test func weatherConfigOverlayDecodesAndDefaultsOn() throws {
    let off = #"{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2,"overlay":false}"#
    #expect(try JSONDecoder().decode(WeatherConfig.self, from: Data(off.utf8)).overlay == false)

    let old = #"{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2}"#
    #expect(try JSONDecoder().decode(WeatherConfig.self, from: Data(old.utf8)).overlay)

    var e = WeatherConfig(enabled: true, latitude: 1, longitude: 2)
    e.overlay = false
    let s = String(decoding: try JSONEncoder().encode(e), as: UTF8.self)
    #expect(s.contains(#""overlay":false"#))
}

@Test func weatherConfigAirFieldsDecodeAndDefault() throws {
    let json = #"""
    {"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2,
     "air_tile":false,"air_popup_threshold":0}
    """#
    let c = try JSONDecoder().decode(WeatherConfig.self, from: Data(json.utf8))
    #expect(c.airTile == false)
    #expect(c.airPopupThreshold == 0)

    let old = #"{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2}"#
    let d = try JSONDecoder().decode(WeatherConfig.self, from: Data(old.utf8))
    #expect(d.airTile)
    #expect(d.airPopupThreshold == 80)

    var e = WeatherConfig(enabled: true, latitude: 1, longitude: 2)
    e.airTile = false
    e.airPopupThreshold = 120
    let data = try JSONEncoder().encode(e)
    let s = String(decoding: data, as: UTF8.self)
    #expect(s.contains("air_tile"))
    #expect(s.contains("air_popup_threshold"))
    let back = try JSONDecoder().decode(WeatherConfig.self, from: data)
    #expect(back == e)
}

@Test func weatherConfigIconIdsRoundTripAndTolerateAbsent() throws {
    let noIcons = #"{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2}"#
    let a = try JSONDecoder().decode(WeatherConfig.self, from: Data(noIcons.utf8))
    #expect(a.iconIds.isEmpty)
    #expect(a.rotateInApps)

    var c = WeatherConfig(enabled: true, latitude: 1, longitude: 2, useNativeIcons: true)
    c.iconIds = ["rain": "999", "storm": "11428"]
    let data = try JSONEncoder().encode(c)
    #expect(String(decoding: data, as: UTF8.self).contains("icon_ids"))
    let back = try JSONDecoder().decode(WeatherConfig.self, from: data)
    #expect(back.iconIds["rain"] == "999")
    #expect(back == c)
}
