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

@Test func weatherConfigIconIdsRoundTripAndTolerateAbsent() throws {
    // Absent icon_ids decodes to an empty map (server omits it when empty).
    let noIcons = #"{"enabled":true,"provider":"open-meteo","latitude":1,"longitude":2}"#
    let a = try JSONDecoder().decode(WeatherConfig.self, from: Data(noIcons.utf8))
    #expect(a.iconIds.isEmpty)
    #expect(a.rotateInApps)   // decodeIfPresent default holds

    // Explicit overrides round-trip.
    var c = WeatherConfig(enabled: true, latitude: 1, longitude: 2, useNativeIcons: true)
    c.iconIds = ["rain": "999", "storm": "11428"]
    let data = try JSONEncoder().encode(c)
    #expect(String(decoding: data, as: UTF8.self).contains("icon_ids"))
    let back = try JSONDecoder().decode(WeatherConfig.self, from: data)
    #expect(back.iconIds["rain"] == "999")
    #expect(back == c)
}

@Test func decodesRemindersConfigSnakeCase() throws {
    let json = #"""
    {"enabled":true,"timezone":"Europe/Amsterdam","popup_duration_seconds":10,
     "use_native_icon":true,"native_icon_id":"1234",
     "items":[{"id":"a","time":"09:00","text":"Stand-up","days":[1,2,3,4,5],"enabled":true,"sound":true},
              {"id":"b","time":"18:30","text":"Walk","days":[],"enabled":false,"sound":false}]}
    """#
    let c = try JSONDecoder().decode(RemindersConfig.self, from: Data(json.utf8))
    #expect(c.enabled)
    #expect(c.timezone == "Europe/Amsterdam")
    #expect(c.popupDurationSeconds == 10)
    #expect(c.useNativeIcon)
    #expect(c.nativeIconId == "1234")
    #expect(c.items.count == 2)
    #expect(c.items[0].id == "a")
    #expect(c.items[0].time == "09:00")
    #expect(c.items[0].days == [1, 2, 3, 4, 5])
    #expect(c.items[0].sound)
    #expect(c.items[1].days.isEmpty)
}

@Test func remindersConfigRoundTrips() throws {
    let c = RemindersConfig(enabled: true, timezone: "UTC",
                            items: [Reminder(id: "x", time: "07:15", text: "Wake", days: [0, 6], sound: true)])
    let data = try JSONEncoder().encode(c)
    let s = String(decoding: data, as: UTF8.self)
    for key in ["popup_duration_seconds", "use_native_icon", "native_icon_id"] {
        #expect(s.contains(key), "encoded reminders config missing key \(key)")
    }
    let back = try JSONDecoder().decode(RemindersConfig.self, from: data)
    #expect(back == c)
}
