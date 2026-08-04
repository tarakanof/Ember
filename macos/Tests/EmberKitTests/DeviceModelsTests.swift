import Testing
import Foundation
@testable import EmberKit

@Test func deviceSettingsDecodesNGKeys() throws {
    // Real shape from the #67 mapping's live device dump.
    let json = ##"{"brightness":128,"volume":10,"autoBrightness":true,"transitionEffect":"Rain","textColor":"#FF8800","timeMode":2}"##
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.brightness == 128)
    #expect(s.volume == 10)
    #expect(s.autoBrightness == true)
    #expect(s.transitionEffect == "Rain")
    #expect(s.textColor == "#FF8800")
    #expect(s.timeMode == 2)
}

@Test func deviceSettingsDecodesNativeAppColorsAndToggles() throws {
    // Issue #92 — confirmed present in the live device's GET /api/v1/settings
    // on firmware 1.0.13.
    let json = #"""
    {"timeColor":"#FFFFFF","dateColor":"#FFFFFF","temperatureColor":"#FF0000",
     "humidityColor":"#00FF00","batteryColor":"#0000FF","useCelsius":true,"smoothScroll":false}
    """#
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.timeColor == "#FFFFFF")
    #expect(s.dateColor == "#FFFFFF")
    #expect(s.temperatureColor == "#FF0000")
    #expect(s.humidityColor == "#00FF00")
    #expect(s.batteryColor == "#0000FF")
    #expect(s.useCelsius == true)
    #expect(s.smoothScroll == false)
}

@Test func deviceSettingsDecodesNestedScrollAndWeekdayBar() throws {
    let json = #"""
    {"scroll":{"mode":"wrap","direction":"left","entry":"inline","whenFits":"static","speed":100,"gap":8,"holdMs":1000},
     "weekdayBar":{"show":true,"startOnMonday":true,"activeColor":"#FFFFFF","inactiveColor":"#666666"}}
    """#
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.scroll?.mode == "wrap")
    #expect(s.scroll?.speed == 100)
    #expect(s.scroll?.holdMs == 1000)
    #expect(s.weekdayBar?.show == true)
    #expect(s.weekdayBar?.startOnMonday == true)
    #expect(s.weekdayBar?.activeColor == "#FFFFFF")
}

@Test func deviceSettingsDecodesDiscreteTimeAndDateFields() throws {
    let json = #"""
    {"time24h":true,"timeLeadingZero":true,"timeShowSeconds":false,"timeShowAmPm":false,
     "timeSeparatorMode":"pulse","dateOrder":"dayMonthYear","dateSeparator":"dot",
     "dateYearMode":"twoDigit","dateShowWeekday":false,"dateMonthNames":false}
    """#
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.time24h == true)
    #expect(s.timeLeadingZero == true)
    #expect(s.timeShowSeconds == false)
    #expect(s.timeSeparatorMode == "pulse")
    #expect(s.dateOrder == "dayMonthYear")
    #expect(s.dateSeparator == "dot")
    #expect(s.dateYearMode == "twoDigit")
}

@Test func deviceSettingsEncodesOnlySetFieldsWithNGKeys() throws {
    var s = DeviceSettings()
    s.brightness = 200
    s.uppercase = false
    let data = try JSONEncoder().encode(s)
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["brightness"] as? Int == 200)
    #expect(obj["uppercase"] as? Bool == false)
    // Unset fields are omitted, so a partial PUT can't clobber other settings.
    #expect(obj["volume"] == nil)
    #expect(obj.count == 2)
}

@Test func deviceSettingsToleratesUnexpectedColorType() throws {
    // The device may return a colour as an [r,g,b] array; that must not break the
    // whole decode — the offending field just becomes nil.
    let json = #"{"brightness":100,"textColor":[255,136,0]}"#
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.brightness == 100)
    #expect(s.textColor == nil)
}

@Test func deviceConfigDecodesSnakeCase() throws {
    let json = #"{"base_url":"http://10.0.0.5","source":"store"}"#
    let c = try JSONDecoder().decode(DeviceConfig.self, from: Data(json.utf8))
    #expect(c.baseURL == "http://10.0.0.5")
    #expect(c.source == "store")
}

@Test func discoverResultDecodes() throws {
    let json = #"{"candidates":[{"host":"awtrix.local.","base_url":"http://10.0.0.9","uid":"u","version":"0.98"}],"effective":"http://10.0.0.9","source":"discovered"}"#
    let r = try JSONDecoder().decode(DiscoverResult.self, from: Data(json.utf8))
    #expect(r.candidates.count == 1)
    #expect(r.candidates[0].baseURL == "http://10.0.0.9")
    #expect(r.candidates[0].id == "http://10.0.0.9")
    #expect(r.source == "discovered")
}

@Test func deviceStatsDecodesNGDeviceFields() throws {
    // Real shape from GET /api/v1/device (task's pinned field list).
    let json = #"""
    {"version":"1.0.13","uid":"e868e705ffb8","hostname":"awtrix","batteryPercent":100,
     "batteryVoltage":4.1,"temperature":24,"humidity":41.5,"lightLevel":230,"brightness":120,
     "fps":24.5,"freeHeapBytes":118772,"uptimeSeconds":4200,"currentApp":"Time"}
    """#
    let s = try JSONDecoder().decode(DeviceStats.self, from: Data(json.utf8))
    #expect(s.version == "1.0.13")
    #expect(s.uid == "e868e705ffb8")
    #expect(s.hostname == "awtrix")
    #expect(s.batteryPercent == 100)
    #expect(s.batteryVoltage == 4.1)
    #expect(s.temperature == 24)
    #expect(s.humidity == 41.5)
    #expect(s.lightLevel == 230)
    #expect(s.brightness == 120)
    #expect(s.uptimeSeconds == 4200)
    #expect(s.currentApp == "Time")
}

@Test func deviceStatsDecodesNestedWifi() throws {
    let json = #"{"version":"1.0.13","wifi":{"ssid":"home","rssi":-52,"ip":"192.168.0.14"}}"#
    let s = try JSONDecoder().decode(DeviceStats.self, from: Data(json.utf8))
    #expect(s.wifi?.ssid == "home")
    #expect(s.wifi?.rssi == -52)
}

@Test func deviceStatsToleratesMissingSensors() throws {
    let json = #"{"version":"1.0.13","batteryPercent":100}"#
    let s = try JSONDecoder().decode(DeviceStats.self, from: Data(json.utf8))
    #expect(s.temperature == nil)
    #expect(s.humidity == nil)
}

@Test func sensorCalibrationDecodesNulls() throws {
    let json = #"{"temp_offset":-7.5,"hum_offset":null}"#
    let c = try JSONDecoder().decode(SensorCalibration.self, from: Data(json.utf8))
    #expect(c.tempOffset == -7.5)
    #expect(c.humOffset == nil)
}

@Test func sensorCalibrationEncodesExplicitNulls() throws {
    // The server treats null as "reset to firmware default" and an absent key
    // as "leave unchanged" — reset-to-default needs the explicit null.
    let data = try JSONEncoder().encode(SensorCalibration(tempOffset: -4))
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["temp_offset"] as? Double == -4)
    #expect(obj.index(forKey: "hum_offset") != nil)
    #expect(obj["hum_offset"] is NSNull)
}

@Test func deviceDisplayDecodesOverlayAndSettings() throws {
    // Real shape from device_display_test.go's fake clock response.
    let json = #"{"overlay":null,"overlaySettings":{"speed":1,"palette":null,"blend":true}}"#
    let d = try JSONDecoder().decode(DeviceDisplay.self, from: Data(json.utf8))
    #expect(d.overlay == nil)
    #expect(d.overlaySettings?.speed == 1)
    #expect(d.overlaySettings?.palette == nil)
    #expect(d.overlaySettings?.blend == true)
}

@Test func deviceDisplayEncodesExplicitNullOverlay() throws {
    // NG has no "clear" value — clearing an overlay is an explicit null, not
    // an omitted key.
    let data = try JSONEncoder().encode(DeviceDisplay(overlay: nil))
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj.index(forKey: "overlay") != nil)
    #expect(obj["overlay"] is NSNull)
}

@Test func deviceDisplayEncodesSetOverlay() throws {
    let data = try JSONEncoder().encode(DeviceDisplay(overlay: "snow"))
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["overlay"] as? String == "snow")
}

@Test func appInfoDecodesNGAppsArray() throws {
    // Real shape from device_display_test.go's fake clock response.
    let json = #"[{"name":"Time","enabled":true,"inLoop":true}]"#
    let apps = try JSONDecoder().decode([AppInfo].self, from: Data(json.utf8))
    #expect(apps.count == 1)
    #expect(apps[0].name == "Time")
    #expect(apps[0].enabled == true)
    #expect(apps[0].inLoop == true)
    #expect(apps[0].id == "Time")
}

@Test func appsUpdateEncodesOrderAndDisabled() throws {
    let u = AppsUpdate(order: ["Time", "Date"], disabled: ["Battery"])
    let data = try JSONEncoder().encode(u)
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["order"] as? [String] == ["Time", "Date"])
    #expect(obj["disabled"] as? [String] == ["Battery"])
}

@Test func deviceCapabilitiesDecodesFullShape() throws {
    let json = #"""
    {"effects":["fire","matrix"],"paletteEffects":["rainbow"],
     "transitions":["slide","fade","zoom"],"overlays":["rain","snow"],
     "palettes":["fire","ice"],"radio":true}
    """#
    let c = try JSONDecoder().decode(DeviceCapabilities.self, from: Data(json.utf8))
    #expect(c.effects == ["fire", "matrix"])
    #expect(c.transitions == ["slide", "fade", "zoom"])
    #expect(c.overlays == ["rain", "snow"])
    #expect(c.radio == true)
}

@Test func deviceCapabilitiesToleratesMissingFields() throws {
    let json = #"{"transitions":["slide"]}"#
    let c = try JSONDecoder().decode(DeviceCapabilities.self, from: Data(json.utf8))
    #expect(c.transitions == ["slide"])
    #expect(c.effects.isEmpty)
    #expect(c.radio == false)
}

@Test func screenFrameDecodesNGEnvelope() throws {
    // Issue #71's live-verification finding: NG wraps the pixel array.
    var pixels = [Int](repeating: 0, count: 256)
    pixels[0] = 0xFF0000
    let data = try JSONEncoder().encode(ScreenFrame(width: 32, height: 8, pixels: pixels))
    let decoded = try JSONDecoder().decode(ScreenFrame.self, from: data)
    #expect(decoded.width == 32)
    #expect(decoded.height == 8)
    #expect(decoded.pixels.count == 256)
    #expect(decoded.pixels[0] == 0xFF0000)
}

@Test func buttonStatusDecodesConfiguredFields() throws {
    let json = #"""
    {"expected_callback":"http://server/hooks/awtrix/button",
     "configured_callback":"http://server/hooks/awtrix/button",
     "configured":true,"last_press_unix":1700000000,"seconds_since":42}
    """#
    let b = try JSONDecoder().decode(ButtonStatus.self, from: Data(json.utf8))
    #expect(b.configuredCallback == "http://server/hooks/awtrix/button")
    #expect(b.configured == true)
    #expect(b.secondsSince == 42)
}

@Test func buttonStatusDecodesNullSecondsSince() throws {
    let json = #"{"expected_callback":"","configured_callback":"","configured":false,"last_press_unix":0,"seconds_since":null}"#
    let b = try JSONDecoder().decode(ButtonStatus.self, from: Data(json.utf8))
    #expect(b.secondsSince == nil)
}

@Test func buttonsUpdateEncodesEnabled() throws {
    let data = try JSONEncoder().encode(ButtonsUpdate(enabled: true))
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["enabled"] as? Bool == true)
}
