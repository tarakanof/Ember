import Testing
import Foundation
@testable import EmberKit

@Test func deviceSettingsDecodesAwtrixKeys() throws {
    let json = ##"{"BRI":128,"VOL":10,"ABRI":true,"TEFF":3,"TIM":true,"TCOL":"#FF8800","TMODE":2}"##
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.bri == 128)
    #expect(s.vol == 10)
    #expect(s.abri == true)
    #expect(s.teff == 3)
    #expect(s.tim == true)
    #expect(s.tcol == "#FF8800")
    #expect(s.tmode == 2)
}

@Test func deviceSettingsEncodesOnlySetFieldsWithAwtrixKeys() throws {
    var s = DeviceSettings()
    s.bri = 200
    s.uppercase = false
    let data = try JSONEncoder().encode(s)
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["BRI"] as? Int == 200)
    #expect(obj["UPPERCASE"] as? Bool == false)
    // Unset fields are omitted, so a partial PUT can't clobber other settings.
    #expect(obj["VOL"] == nil)
    #expect(obj.count == 2)
}

@Test func deviceSettingsToleratesUnexpectedColorType() throws {
    // The device may return a colour as an [r,g,b] array; that must not break the
    // whole decode — the offending field just becomes nil.
    let json = #"{"BRI":100,"TCOL":[255,136,0]}"#
    let s = try JSONDecoder().decode(DeviceSettings.self, from: Data(json.utf8))
    #expect(s.bri == 100)
    #expect(s.tcol == nil)
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

@Test func deviceStatsDecodesLiveBrightness() throws {
    let json = #"{"bat":100,"version":"0.98","ram":118772,"bri":2}"#
    let s = try JSONDecoder().decode(DeviceStats.self, from: Data(json.utf8))
    #expect(s.bat == 100)
    #expect(s.bri == 2)
}

@Test func deviceStatsDecodesSensorReadings() throws {
    // temp arrives as an int when temp_dec_places is 0; both must decode.
    let json = #"{"bat":100,"temp":24,"hum":41.5,"lux":230}"#
    let s = try JSONDecoder().decode(DeviceStats.self, from: Data(json.utf8))
    #expect(s.temp == 24)
    #expect(s.hum == 41.5)
    #expect(s.lux == 230)
}

@Test func deviceStatsToleratesMissingSensors() throws {
    // sensor_reading:false omits temp/hum entirely.
    let json = #"{"bat":100,"version":"0.98"}"#
    let s = try JSONDecoder().decode(DeviceStats.self, from: Data(json.utf8))
    #expect(s.temp == nil)
    #expect(s.hum == nil)
}

@Test func sensorCalibrationDecodesNulls() throws {
    let json = #"{"temp_offset":-7.5,"hum_offset":null}"#
    let c = try JSONDecoder().decode(SensorCalibration.self, from: Data(json.utf8))
    #expect(c.tempOffset == -7.5)
    #expect(c.humOffset == nil)
}

@Test func sensorCalibrationEncodesExplicitNulls() throws {
    // The server treats null as "remove the key from dev.json" and an absent
    // key as "leave unchanged" — reset-to-default needs the explicit null.
    let data = try JSONEncoder().encode(SensorCalibration(tempOffset: -4))
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["temp_offset"] as? Double == -4)
    #expect(obj.index(forKey: "hum_offset") != nil)
    #expect(obj["hum_offset"] is NSNull)
}
