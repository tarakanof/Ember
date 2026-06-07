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
                "popup_interval_minutes", "popup_duration_seconds",
                "popup_on_change", "severe_alert", "severe_sound", "use_native_icons"] {
        #expect(s.contains(key), "encoded weather config missing key \(key)")
    }
    // Round-trips back to an equal value.
    let back = try JSONDecoder().decode(WeatherConfig.self, from: data)
    #expect(back == c)
}

