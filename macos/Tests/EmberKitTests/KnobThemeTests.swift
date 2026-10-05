import Foundation
import ImageIO
import Testing
@testable import EmberKit

// The drift guard for knob-theme.json. Every key is pinned here, so a change
// to the file has to be made on purpose (and copied to cinder, tarakanof/cinder#34).
// `themeMatchesCinderSource` also reads the firmware source when a cinder
// checkout sits next to this repo (or CINDER_DIR names one) and checks each
// value against the constant it mirrors.

private let pinned: [String: String] = [
"version": "1",
    "screen.diameter_px": "466",
    "font.family": "Montserrat-Medium",
    "font.file": "Montserrat-Medium.ttf",
    "font.metrics.14.line_height_px": "16",
    "font.metrics.14.baseline_px": "3",
    "font.metrics.18.line_height_px": "21",
    "font.metrics.18.baseline_px": "4",
    "font.metrics.24.line_height_px": "27",
    "font.metrics.24.baseline_px": "5",
    "font.metrics.30.line_height_px": "33",
    "font.metrics.30.baseline_px": "6",
    "font.metrics.48.line_height_px": "52",
    "font.metrics.48.baseline_px": "9",
    "mood_colors.idle": "#888888",
    "mood_colors.working": "#2EE85E",
    "mood_colors.waiting": "#FFC14D",
    "mood_colors.error": "#FF3A3A",
    "mood_colors.done": "#4FA9FF",
    "bot.fill": "0.84",
    "bot.body_color": "#000000",
    "bot.eye_color": "#F4F4F2",
    "bot.eye_scale": "1.15",
    "bot.hop_scale": "0.45",
    "bot.rim_px": "6",
    "bot.rim_dim_gain": "0.55",
    "bot.rim_steps": "5",
    "bot.ring_points": "192",
    "bot.triangle.radius": "1.1",
    "bot.triangle.sagitta": "0.11",
    "bot.triangle.table_bins": "720",
    "bot.triangle.blur_deg": "1.2",
    "bot.hop.duration_s": "1",
    "bot.hop.squash": "0.75",
    "bot.hop.interval_median_s": "4.5",
    "bot.hop.interval_sigma": "0.35",
    "bot.hop.interval_min_s": "2.5",
    "bot.hop.interval_max_s": "9",
    "bot.eyes.rest_x": "0.67",
    "bot.eyes.reach": "0.6",
    "bot.eyes.reach_triangle": "-0.2",
    "bot.eyes.drop_triangle": "0.12",
    "bot.eyes.drop_slump": "0.1",
    "bot.eyes.limit": "0.52",
    "bot.eyes.limit_triangle": "-0.15",
    "bot.eyes.foreshorten": "0.45",
    "bot.eyes.straight": "0.06",
    "bot.eyes.lean_at": "0.6",
    "bot.eyes.sep": "0.44",
    "bot.eyes.sep_round": "0.5",
    "bot.eyes.rise": "0.04",
    "bot.eyes.min_half": "0.002",
    "bot.eyes.dash.width": "[0.14,0.24]",
    "bot.eyes.dash.height": "[0.38,0.06]",
    "bot.eyes.dash.angle_deg": "[27,-6]",
    "bot.eyes.angry.width": "[0.13,0.22]",
    "bot.eyes.angry.height": "[0.3,0.06]",
    "bot.eyes.angry.angle_deg": "[55,10]",
    "bot.eyes.round.width": "[0.3,0.34]",
    "bot.eyes.round.height": "[0.44,0.05]",
    "bot.eyes.round.angle_deg": "[10,10]",
    "bot.eyes.happy.width": "0.3",
    "bot.eyes.happy.height": "[0.16,0.03]",
    "bot.eyes.happy.lift": "1.5",
    "bot.eyes.happy.stroke": "0.11",
    "bot.eyes.happy.points": "7",
    "bot.badge.at": "0.98",
    "bot.badge.gap": "0.3",
    "bot.badge.dot": "0.22",
    "bot.host.font_px": "30",
    "bot.host.radius_px": "176",
    "bot.host.icon_cell_px": "4",
    "bot.host.mark_h_px": "48",
    "bot.host.mark_bottom_px": "146",
    "bot.host.claude_color": "#D77757",
    "bot.host.codex_color": "#FFFFFF",
    "bot.host.max_chars": "10",
    "bot.glint.width_px": "12",
    "bot.glint.tail_deg": "40",
    "bot.glint.white_mix": "0.85",
    "bot.glint.period_s": "3",
    "bot.glint.fps": "15",
    "pomodoro.ring_radius_px": "222",
    "pomodoro.ring_width_px": "12",
    "pomodoro.track_gain": "0.18",
    "pomodoro.paused_gain": "0.45",
    "pomodoro.colors.focus": "#FF6A3D",
    "pomodoro.colors.break": "#4FA9FF",
    "pomodoro.colors.other": "#9A9A9A",
    "pomodoro.colors.idle_track": "#262626",
    "pomodoro.colors.text": "#F4F4F2",
    "pomodoro.colors.text_dim": "#5A5A5A",
    "pomodoro.colors.note": "#9A9A9A",
    "pomodoro.time.font_px": "48",
    "pomodoro.time.dy_px": "-6",
    "pomodoro.time.width_px": "260",
    "pomodoro.phase.font_px": "24",
    "pomodoro.phase.dy_px": "46",
    "pomodoro.phase.width_px": "300",
    "pomodoro.round.font_px": "14",
    "pomodoro.round.dy_px": "-54",
    "pomodoro.round.width_px": "200",
    "weather.sky.width_px": "200",
    "weather.sky.height_px": "140",
    "weather.sky.y_px": "96",
    "weather.temp.font_px": "48",
    "weather.temp.color": "#8C8C8C",
    "weather.temp.still_color": "#5A5A5A",
    "weather.max_age_s": "1800",
    "weather.colors.sun": "#E0A030",
    "weather.colors.rays": "#C08828",
    "weather.colors.moon": "#D8D0B0",
    "weather.colors.star": "#C8D0E0",
    "weather.colors.cloud": "#9AA4B0",
    "weather.colors.rain_cloud": "#7C8694",
    "weather.colors.storm_cloud": "#6A7280",
    "weather.colors.lit_cloud": "#C8CCD8",
    "weather.colors.snow_cloud": "#A8B0BC",
    "weather.colors.rain": "#5C9CE0",
    "weather.colors.snow": "#D0DCE8",
    "weather.colors.bolt": "#F0D040",
    "weather.colors.fog": "#8890A0",
    "weather.colors.rime": "#90C8E0",
    "weather.colors.still": "#5A5A5A",
    "weather.scene.fps.rain": "12",
    "weather.scene.fps.snow": "10",
    "weather.scene.fps.other": "4",
    "weather.scene.max_step_s": "0.5",
    "weather.scene.sprites.rain": "[8,18]",
    "weather.scene.sprites.flake_s": "[6,6]",
    "weather.scene.sprites.flake_l": "[14,14]",
    "weather.scene.sprites.cloud_l": "[110,56]",
    "weather.scene.sprites.cloud_s": "[68,36]",
    "weather.scene.sprites.bolt": "[20,40]",
    "weather.scene.sprites.moon": "[40,40]",
    "weather.scene.sprites.star": "[10,10]",
    "weather.scene.sprites.sun": "[36,36]",
    "weather.scene.sprites.rays": "[60,60]",
    "weather.scene.sprites.fog": "[128,10]",
    "weather.scene.ray_frames": "16",
    "weather.scene.ray_steps_per_s": "4",
    "weather.scene.drops.rain": "[5,10,16]",
    "weather.scene.drops.storm": "[10,10,14]",
    "weather.scene.drops.snow": "[6,9,12]",
    "weather.scene.drop_alpha.rain": "[170,210,255]",
    "weather.scene.drop_alpha.snow": "230",
    "weather.scene.rain.top": "50",
    "weather.scene.rain.x": "[58,154]",
    "weather.scene.rain.speed": "150",
    "weather.scene.rain.speed_spread": "0.2",
    "weather.scene.rain.slant": "[-4,14]",
    "weather.scene.rain.respawn_band": "6",
    "weather.scene.snow.x": "[40,160]",
    "weather.scene.snow.amp": "[4,4]",
    "weather.scene.snow.freq": "[0.5,0.4]",
    "weather.scene.snow.speed": "[18,14]",
    "weather.scene.snow.large_share": "0.4",
    "weather.scene.stars.x": "[28,158,44,150,178,14]",
    "weather.scene.stars.y": "[26,18,98,104,60,62]",
    "weather.scene.stars.jitter": "8",
    "weather.scene.stars.alpha": "90",
    "weather.scene.stars.twinkle_alpha": "165",
    "weather.scene.stars.first_twinkle_s": "1",
    "weather.scene.stars.twinkle_s": "[0.8,0.8]",
    "weather.scene.bolt.first_s": "[2,3]",
    "weather.scene.bolt.gap_s": "[4,6]",
    "weather.scene.bolt.on_s": "0.25",
    "weather.scene.bolt.double_chance": "0.5",
    "weather.scene.bolt.second_s": "[0.4,0.6]",
    "weather.scene.bolt.x": "[60,70]",
    "weather.scene.bolt.y": "50",
    "weather.scene.layout.clear_sun": "[100,70]",
    "weather.scene.layout.night_moon": "[80,46]",
    "weather.scene.layout.partly_moon": "[48,30]",
    "weather.scene.layout.partly_sun": "[70,52]",
    "weather.scene.layout.partly_cloud.x": "74",
    "weather.scene.layout.partly_cloud.y": "62",
    "weather.scene.layout.partly_cloud.amp": "14",
    "weather.scene.layout.partly_cloud.period_s": "40",
    "weather.scene.layout.partly_cloud.phase": "0",
    "weather.scene.layout.partly_cloud.alpha": "255",
    "weather.scene.layout.overcast_back.x": "22",
    "weather.scene.layout.overcast_back.y": "24",
    "weather.scene.layout.overcast_back.amp": "10",
    "weather.scene.layout.overcast_back.period_s": "50",
    "weather.scene.layout.overcast_back.phase": "1",
    "weather.scene.layout.overcast_back.alpha": "150",
    "weather.scene.layout.overcast_front.x": "66",
    "weather.scene.layout.overcast_front.y": "52",
    "weather.scene.layout.overcast_front.amp": "16",
    "weather.scene.layout.overcast_front.period_s": "36",
    "weather.scene.layout.overcast_front.phase": "0",
    "weather.scene.layout.overcast_front.alpha": "255",
    "weather.scene.layout.precip_cloud.x": "45",
    "weather.scene.layout.precip_cloud.y": "4",
    "weather.scene.layout.precip_cloud.amp": "6",
    "weather.scene.layout.precip_cloud.period_s": "30",
    "weather.scene.layout.precip_cloud.phase": "0",
    "weather.scene.layout.precip_cloud.alpha": "255",
    "weather.scene.layout.fog.x": "36",
    "weather.scene.layout.fog.y": "30",
    "weather.scene.layout.fog.step": "22",
    "weather.scene.layout.fog.amp": "[28,22,30,18]",
    "weather.scene.layout.fog.period_s": "[23,31,19,27]",
    "weather.scene.layout.fog.phase": "[0,2.1,4,1.2]",
    "weather.scene.layout.fog.alpha": "[210,140]",
    "nowplaying.backdrop_disk_radius_px": "188",
    "nowplaying.ring_radius_px": "198",
    "nowplaying.ring_width_px": "6",
    "nowplaying.album_px": "240",
    "nowplaying.album_dy_px": "-44",
    "nowplaying.artist_px": "64",
    "nowplaying.dot_px": "16",
    "nowplaying.colors.track": "#2E2E2E",
    "nowplaying.colors.arc": "#F4F4F2",
    "nowplaying.colors.arc_paused": "#6E6E6E",
    "nowplaying.colors.text": "#F4F4F2",
    "nowplaying.colors.sub": "#BDBDBD",
    "nowplaying.colors.meta": "#8A8A8A",
    "nowplaying.colors.idle": "#5A5A5A",
    "nowplaying.colors.placeholder": "#1C1C1C",
    "nowplaying.colors.note": "#4A4A4A",
    "nowplaying.title.font_px": "24",
    "nowplaying.title.dy_px": "96",
    "nowplaying.title.width_px": "290",
    "nowplaying.sub.font_px": "18",
    "nowplaying.sub.dy_px": "126",
    "nowplaying.sub.width_px": "260",
    "nowplaying.meta.font_px": "14",
    "nowplaying.meta.dy_px": "150",
    "nowplaying.meta.width_px": "220",
    "nowplaying.idle.font_px": "24",
    "nowplaying.idle.dy_px": "0",
    "nowplaying.idle.width_px": "300",
]

private func themeJSON() throws -> [String: Any] {
    let url = try #require(KnobTheme.jsonURL)
    return try #require(JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any])
}

private func format(_ v: Any) -> String {
    switch v {
    case let s as String: return s
    case let a as [Any]: return "[" + a.map(format).joined(separator: ",") + "]"
    case let n as NSNumber:
        let d = n.doubleValue
        return d == d.rounded() && abs(d) < 1e15 ? String(Int(d)) : "\(d)"
    default: return "\(v)"
    }
}

private func flatten(_ v: Any, _ prefix: String = "", into out: inout [String: String]) {
    if let d = v as? [String: Any] {
        for (k, x) in d { flatten(x, prefix.isEmpty ? k : prefix + "." + k, into: &out) }
    } else {
        out[prefix] = format(v)
    }
}

@Test func knobThemePinsEveryKey() throws {
    var flat: [String: String] = [:]
    flatten(try themeJSON(), into: &flat)
    for key in Set(flat.keys).union(pinned.keys).sorted() {
        #expect(flat[key] == pinned[key], "knob-theme.json \(key)")
    }
    let t = try KnobTheme.load()
    for s in KnobWeatherScene.Sprite.allCases {
        #expect(t.weather.scene.sprites[s.rawValue]?.count == 2, "sprite size for \(s)")
    }
    #expect(t.weather.scene.rain.slant[0] / t.weather.scene.rain.slant[1] == KnobWeatherScene.rainSlant)
    #expect(KnobTheme.fontURL != nil)
}

@Test func knobThemeRejectsMissingKeysAndBadColours() {
    #expect(throws: (any Error).self) { try KnobTheme.decode(Data(#"{"version":1}"#.utf8)) }
    #expect(throws: (any Error).self) { try JSONDecoder().decode([RGB].self, from: Data(#"["red"]"#.utf8)) }
    #expect((try? JSONDecoder().decode([RGB].self, from: Data(##"["#0A0B0C"]"##.utf8))) == [RGB(r: 10, g: 11, b: 12)])
}

// MARK: Parity with cinder's source (opt-in)

private let cinderDir: URL? = {
    let fm = FileManager.default
    var candidates: [URL] = []
    if let env = ProcessInfo.processInfo.environment["CINDER_DIR"] { candidates.append(URL(fileURLWithPath: env)) }
    var root = URL(fileURLWithPath: #filePath)
    for _ in 0..<4 { root.deleteLastPathComponent() }
    candidates.append(root.deletingLastPathComponent().appendingPathComponent("cinder"))
    return candidates.first { fm.fileExists(atPath: $0.appendingPathComponent("firmware/main/bot_view.c").path) }
}()

/// One firmware constant: `pattern`'s capture groups, mapped to the theme
/// keys they mirror (`map` turns the groups into each key's value).
private struct Check {
    let file: String
    let pattern: String
    let keys: [(String, ([String]) -> String)]
}

private func n(_ s: String) -> String {
    var t = s.replacingOccurrences(of: "f", with: "")
    if t.hasPrefix("0x") { return "#" + t.dropFirst(2).uppercased() }
    if t.hasPrefix("(") { t.removeFirst() }
    return format(NSNumber(value: Double(t) ?? .nan))
}
private func g(_ i: Int) -> ([String]) -> String { { n($0[i]) } }
private func arr(_ idx: Int...) -> ([String]) -> String { { c in "[" + idx.map { n(c[$0]) }.joined(separator: ",") + "]" } }
private func list(_ i: Int) -> ([String]) -> String {
    { c in "[" + c[i].split(separator: ",").map { n($0.trimmingCharacters(in: .whitespaces)) }.joined(separator: ",") + "]" }
}
private func neg(_ i: Int) -> ([String]) -> String { { "-" + n($0[i]) } }
private func one(_ file: String, _ pattern: String, _ key: String) -> Check { Check(file: file, pattern: pattern, keys: [(key, g(1))]) }

private let num = #"(-?[\d.]+f?)"#
private let hex = #"(0x[0-9A-Fa-f]{6})"#
private let bv = "main/bot_view.c", bs = "components/bot/bot_shape.c", bb = "components/bot/bot_behavior.c"
private let nv = "main/nowplaying_view.c", nc = "main/nowplaying_client.c", nh = "components/nowplaying/include/np.h"
private let pv = "main/pomo_view.c", wv = "main/weather_view.c", ws = "components/weather/weather_scene.c"

private func allChecks() -> [Check] {
    var c: [Check] = [
        one(bv, #"#define FILL "# + num, "bot.fill"),
        one(bv, #"#define HOP_SCALE "# + num, "bot.hop_scale"),
        one(bv, #"#define EYE_SCALE "# + num, "bot.eye_scale"),
        one(bv, #"#define RIM_PX "# + num, "bot.rim_px"),
        one(bv, #"#define RIM_STEPS (\d+)"#, "bot.rim_steps"),
        one(bv, #"#define LABEL_R "# + num, "bot.host.radius_px"),
        one(bv, #"#define LABEL_ICON_CELL (\d+)"#, "bot.host.icon_cell_px"),
        one(bv, #"#define LABEL_ICON_R (\d+)"#, "bot.host.mark_bottom_px"),
        one("components/bot/include/tool_marks.h", #"#define TOOL_MARK_H (\d+)"#, "bot.host.mark_h_px"),
        one("components/bot/tool_marks.c", #"case 1: \*rgb = "# + hex, "bot.host.claude_color"),
        one("components/bot/tool_marks.c", #"case 2: \*rgb = "# + hex, "bot.host.codex_color"),
        Check(file: bv, pattern: #"#define GLINT_HW "# + num, keys: [("bot.glint.width_px", { n(String(2 * (Double(n($0[1])) ?? .nan))) })]),
        one(bv, #"#define GLINT_TAIL_DEG "# + num, "bot.glint.tail_deg"),
        one(bv, #"#define GLINT_MIX "# + num, "bot.glint.white_mix"),
        one(bv, #"#define GLINT_PERIOD_S "# + num, "bot.glint.period_s"),
        one(bv, #"#define GLINT_FPS (\d+)"#, "bot.glint.fps"),
        one(bv, #"dim \? "# + num, "bot.rim_dim_gain"),
        one(bv, hex + #", 1\.0f\);"#, "bot.eye_color"),
        one(bv, #"const font = &lv_font_montserrat_(\d+)"#, "bot.host.font_px"),
        one(bv, #"cos\(M_PI / 4\) \* "# + num, "bot.badge.at"),
        one(bv, #"gap = \(int\)lround\("# + num, "bot.badge.gap"),
        one(bv, #"dot = \(int\)lround\("# + num, "bot.badge.dot"),
        one(bv, #"case BOT_WORKING: return "# + hex, "mood_colors.working"),
        one(bv, #"case BOT_WAITING: return "# + hex, "mood_colors.waiting"),
        one(bv, #"case BOT_ERROR:   return "# + hex, "mood_colors.error"),
        one(bv, #"case BOT_DONE:    return "# + hex, "mood_colors.done"),
        one(bv, #"default:          return "# + hex, "mood_colors.idle"),
        one("components/bot/include/bot_shape.h", #"#define BOT_RING_POINTS (\d+)"#, "bot.ring_points"),
        one(bs, #"#define TRI_TABLE (\d+)"#, "bot.triangle.table_bins"),
        Check(file: bs, pattern: #"const double R = "# + num + ", SAG = " + num,
              keys: [("bot.triangle.radius", g(1)), ("bot.triangle.sagitta", g(2))]),
        one(bs, #"double sig = "# + num + " / 360", "bot.triangle.blur_deg"),
        one(bs, #"#define REST_X "# + num, "bot.eyes.rest_x"),
        Check(file: bs, pattern: #"double reach = "# + num + #" - "# + num + #" \* p->triangle"#,
              keys: [("bot.eyes.reach", g(1)), ("bot.eyes.reach_triangle", neg(2))]),
        Check(file: bs, pattern: #"p->gaze_y \* reach - "# + num + #" \* p->triangle - "# + num,
              keys: [("bot.eyes.drop_triangle", g(1)), ("bot.eyes.drop_slump", g(2))]),
        Check(file: bs, pattern: #"double limit = "# + num + #" - "# + num,
              keys: [("bot.eyes.limit", g(1)), ("bot.eyes.limit_triangle", neg(2))]),
        one(bs, #"sqrt\(1 - "# + num + #" \* cx \* cx\)"#, "bot.eyes.foreshorten"),
        one(bs, #"const double straight = "# + num, "bot.eyes.straight"),
        one(bs, #"REST_X \* "# + num + " - straight", "bot.eyes.lean_at"),
        Check(file: bs, pattern: #"BOT_EYES_ROUND \? "# + num + " : " + num,
              keys: [("bot.eyes.sep_round", g(1)), ("bot.eyes.sep", g(2))]),
        one(bs, #"BOT_EYES_DASH \? "# + num + #" \* side"#, "bot.eyes.rise"),
        one(bs, #"if \(half < "# + num, "bot.eyes.min_half"),
        Check(file: bs, pattern: #"double w = "# + num + #" \* ffx, h = L\("# + num + ", " + num + #"\) \* ffy"#,
              keys: [("bot.eyes.happy.width", g(1)), ("bot.eyes.happy.height", arr(2, 3))]),
        one(bs, #"qy = ey \+ h \* "# + num, "bot.eyes.happy.lift"),
        one(bs, #"s->n = (\d+);   /\* 6 segments"#, "bot.eyes.happy.points"),
        one(bs, #"s->width = "# + num + #" \* fmin"#, "bot.eyes.happy.stroke"),
        Check(file: bb, pattern: #"next_hop_at = t \+ lognormal\(b, "# + num + ", " + num + ", " + num + ", " + num,
              keys: [("bot.hop.interval_median_s", g(1)), ("bot.hop.interval_sigma", g(2)),
                     ("bot.hop.interval_min_s", g(3)), ("bot.hop.interval_max_s", g(4))]),
        one("components/bot/include/bot_behavior.h", #"#define BOT_HOP_DURATION_S "# + num, "bot.hop.duration_s"),
        one("components/bot/include/bot_behavior.h", #"#define BOT_HOP_SQUASH "# + num, "bot.hop.squash"),
        one("components/ember_host/include/ember_host.h", #"#define EMBER_HOST_MAX (\d+)"#, "bot.host.max_chars"),
        one(pv, #"#define TRACK_GAIN "# + num, "pomodoro.track_gain"),
        one(pv, #"#define PAUSED_GAIN "# + num, "pomodoro.paused_gain"),
        Check(file: pv, pattern: #"\.r = (\d+), \.hw = (\d+)"#,
              keys: [("pomodoro.ring_radius_px", g(1)), ("pomodoro.ring_width_px", { n(String(2 * (Int($0[2]) ?? 0))) })]),
        one(wv, #"#define TEMP_RGB "# + hex, "weather.temp.color"),
        one(wv, #"#define TEMP_STILL_RGB "# + hex, "weather.temp.still_color"),
        one(wv, #"#define PAGE_Y (\d+)"#, "weather.sky.y_px"),
        one(wv, #"s_temp, &lv_font_montserrat_(\d+)"#, "weather.temp.font_px"),
        one("components/weather/include/weather_scene.h", #"#define WX_SKY_W (\d+)"#, "weather.sky.width_px"),
        one("components/weather/include/weather_scene.h", #"#define WX_SKY_H (\d+)"#, "weather.sky.height_px"),
        one("components/weather/include/weather_scene.h", #"#define WX_RAY_STEPS (\d+)"#, "weather.scene.ray_frames"),
        one("components/weather/include/weather_face.h", #"#define WX_MAX_AGE_S "# + num, "weather.max_age_s"),
        one(ws, #"#define STILL_RGB "# + hex, "weather.colors.still"),
        one(ws, #"case WX_STORM: return 1\.0 / (\d+);"#, "weather.scene.fps.rain"),
        one(ws, #"case WX_SNOW: return 1\.0 / (\d+);"#, "weather.scene.fps.snow"),
        one(ws, #"default: return 1\.0 / (\d+);"#, "weather.scene.fps.other"),
        one(ws, #"s->since > "# + num + " \\?", "weather.scene.max_step_s"),
        one(ws, #"fmod\(s->t \* "# + num + ", WX_RAY_STEPS", "weather.scene.ray_steps_per_s"),
        Check(file: ws, pattern: #"WX_RAIN\) return l->intensity == WX_INT_LIGHT \? (\d+) : l->intensity == WX_INT_HEAVY \? (\d+) : (\d+);"#,
              keys: [("weather.scene.drops.rain", arr(1, 3, 2))]),
        Check(file: ws, pattern: #"WX_STORM\) return l->intensity == WX_INT_HEAVY \? (\d+) : (\d+);"#,
              keys: [("weather.scene.drops.storm", arr(2, 2, 1))]),
        Check(file: ws, pattern: #"WX_SNOW\) return l->intensity == WX_INT_LIGHT \? (\d+) : l->intensity == WX_INT_HEAVY \? (\d+) : (\d+);"#,
              keys: [("weather.scene.drops.snow", arr(1, 3, 2))]),
        one(ws, #"particles\(&e, s, SNOW_RGB, (\d+)\)"#, "weather.scene.drop_alpha.snow"),
        Check(file: ws, pattern: #"RAIN_RGB, l->intensity == WX_INT_LIGHT \? (\d+) : l->intensity == WX_INT_HEAVY \? (\d+) : (\d+)\)"#,
              keys: [("weather.scene.drop_alpha.rain", arr(1, 3, 2))]),
        one(ws, #"#define DROP_TOP "# + num, "weather.scene.rain.top"),
        one(ws, #"#define DROP_TOP "# + num, "weather.scene.bolt.y"),
        Check(file: ws, pattern: #"#define DROP_X0 "# + num + #"\s+#define DROP_X1 "# + num, keys: [("weather.scene.rain.x", arr(1, 2))]),
        Check(file: ws, pattern: #"#define FLAKE_X0 "# + num + #"\s+#define FLAKE_X1 "# + num, keys: [("weather.scene.snow.x", arr(1, 2))]),
        Check(file: ws, pattern: #"p->vy = (\d+) \* \("# + num + " \\+ " + num + #" \* frand"#,
              keys: [("weather.scene.rain.speed", g(1)), ("weather.scene.rain.speed_spread", g(3))]),
        Check(file: ws, pattern: #"RAIN_SLANT \("# + num + " / " + num + #"\)"#, keys: [("weather.scene.rain.slant", arr(1, 2))]),
        one(ws, #"DROP_TOP \+ (\d+) \* frand\(s\);"#, "weather.scene.rain.respawn_band"),
        Check(file: ws, pattern: #"p->amp = (\d+) \+ (\d+) \* frand"#, keys: [("weather.scene.snow.amp", arr(1, 2))]),
        Check(file: ws, pattern: #"p->freq = "# + num + " \\+ " + num, keys: [("weather.scene.snow.freq", arr(1, 2))]),
        Check(file: ws, pattern: #"p->vy = (\d+) \+ (\d+) \* frand"#, keys: [("weather.scene.snow.speed", arr(1, 2))]),
        one(ws, #"frand\(s\) < "# + num + #" \? WX_SPR_FLAKE_L"#, "weather.scene.snow.large_share"),
        Check(file: ws, pattern: #"SX\[WX_STARS\] = \{([^}]*)\}, SY\[WX_STARS\] = \{([^}]*)\}"#,
              keys: [("weather.scene.stars.x", list(1)), ("weather.scene.stars.y", list(2))]),
        one(ws, #"\(int\)\(frand\(s\) \* (\d+)\) - \d+;"#, "weather.scene.stars.jitter"),
        one(ws, #"int a = (\d+);"#, "weather.scene.stars.alpha"),
        one(ws, #"a \+= \(int\)\((\d+) \*"#, "weather.scene.stars.twinkle_alpha"),
        one(ws, #"next_twinkle = s->t \+ "# + num + ";", "weather.scene.stars.first_twinkle_s"),
        Check(file: ws, pattern: #"next_twinkle = s->t \+ "# + num + " \\+ " + num + #" \* frand"#,
              keys: [("weather.scene.stars.twinkle_s", arr(1, 2))]),
        Check(file: ws, pattern: #"schedule_bolt\(s, s->t, (\d+), (\d+)\);\s+s->acc"#, keys: [("weather.scene.bolt.first_s", arr(1, 2))]),
        Check(file: ws, pattern: #"schedule_bolt\(s, s->t, (\d+), (\d+)\);\s+\}"#, keys: [("weather.scene.bolt.gap_s", arr(1, 2))]),
        one(ws, #"bolt_off = s->t \+ "# + num + ";", "weather.scene.bolt.on_s"),
        one(ws, #"if \(frand\(s\) < "# + num + #"\) \{\s+s->bolt_on2"#, "weather.scene.bolt.double_chance"),
        Check(file: ws, pattern: #"bolt_on2 = s->t \+ "# + num + #";\s+s->bolt_off2 = s->t \+ "# + num + ";",
              keys: [("weather.scene.bolt.second_s", arr(1, 2))]),
        Check(file: ws, pattern: #"bolt_x = (\d+) \+ \(int\)\(frand\(s\) \* (\d+)\)"#, keys: [("weather.scene.bolt.x", arr(1, 2))]),
        Check(file: ws, pattern: #"case WX_CLEAR_DAY:\s+sun\(&e, s, (\d+), (\d+)\)"#, keys: [("weather.scene.layout.clear_sun", arr(1, 2))]),
        Check(file: ws, pattern: #"emit\(&e, WX_SPR_MOON, 0, (\d+), (\d+), MOON_RGB, 255\);\s+break;"#,
              keys: [("weather.scene.layout.night_moon", arr(1, 2))]),
        Check(file: ws, pattern: #"if \(l->night\) emit\(&e, WX_SPR_MOON, 0, (\d+), (\d+)"#, keys: [("weather.scene.layout.partly_moon", arr(1, 2))]),
        Check(file: ws, pattern: #"else sun\(&e, s, (\d+), (\d+)\)"#, keys: [("weather.scene.layout.partly_sun", arr(1, 2))]),
        Check(file: ws, pattern: #"static const float A\[4\] = \{([^}]*)\}, P\[4\] = \{([^}]*)\}, PH\[4\] = \{([^}]*)\}"#,
              keys: [("weather.scene.layout.fog.amp", list(1)), ("weather.scene.layout.fog.period_s", list(2)),
                     ("weather.scene.layout.fog.phase", list(3))]),
        Check(file: ws, pattern: #"WX_SPR_FOG, 0, (\d+) \+ drift\(t, A\[i\], P\[i\], PH\[i\]\), (\d+) \+ "# + num + #" \* i"#,
              keys: [("weather.scene.layout.fog.x", g(1)), ("weather.scene.layout.fog.y", g(2)), ("weather.scene.layout.fog.step", g(3))]),
        Check(file: ws, pattern: #"i % 2 \? (\d+) : (\d+)\)"#, keys: [("weather.scene.layout.fog.alpha", arr(2, 1))]),
        one(ws, #"#define CLOUD_TOP_Y (\d+)"#, "weather.scene.layout.precip_cloud.y"),
        Check(file: ws, pattern: #"cloud\(&e, WX_SPR_CLOUD_L, (\d+) \+ drift\(t, (\d+), (\d+), (\d+)\), CLOUD_TOP_Y, crgb, (\d+)\)"#,
              keys: [("weather.scene.layout.precip_cloud.x", g(1)), ("weather.scene.layout.precip_cloud.amp", g(2)),
                     ("weather.scene.layout.precip_cloud.period_s", g(3)), ("weather.scene.layout.precip_cloud.phase", g(4)),
                     ("weather.scene.layout.precip_cloud.alpha", g(5))]),
    ]
    func drift(_ sprite: String, _ key: String, after: String) -> Check {
        Check(file: ws, pattern: after + #"[^;]*?cloud\(&e, WX_SPR_"# + sprite + #", (\d+) \+ drift\(t, (\d+), (\d+), (\d+)\), (\d+), CLOUD_RGB, (\d+)\)"#,
              keys: [("x", 1), ("amp", 2), ("period_s", 3), ("phase", 4), ("y", 5), ("alpha", 6)].map {
                  ("weather.scene.layout.\(key).\($0.0)", g($0.1)) })
    }
    c.append(drift("CLOUD_L", "partly_cloud", after: #"else sun\(&e, s, \d+, \d+\);\s+"#))
    c.append(drift("CLOUD_S", "overcast_back", after: #"case WX_OVERCAST:\s+"#))
    c.append(drift("CLOUD_L", "overcast_front", after: #"case WX_OVERCAST:\s+cloud[^;]*;\s+"#))
    for (key, def) in [("focus", "FOCUS"), ("break", "BREAK"), ("other", "OTHER"), ("idle_track", "IDLE_TRACK"),
                       ("text", "TEXT"), ("text_dim", "TEXT_DIM"), ("note", "NOTE")] {
        c.append(one(pv, "#define COL_\(def) " + hex, "pomodoro.colors.\(key)"))
    }
    c += [
        one(nc, #"#define FACE_DISK_R "# + num, "nowplaying.backdrop_disk_radius_px"),
        Check(file: nv, pattern: #"\.r = (\d+), \.hw = (\d+)"#,
              keys: [("nowplaying.ring_radius_px", g(1)), ("nowplaying.ring_width_px", { n(String(2 * (Int($0[2]) ?? 0))) })]),
        one(nh, #"#define NP_ALBUM_PX (\d+)"#, "nowplaying.album_px"),
        one(nh, #"#define NP_ARTIST_PX (\d+)"#, "nowplaying.artist_px"),
        one(nv, #"#define ALBUM_DY \((-?\d+)\)"#, "nowplaying.album_dy_px"),
        one(nv, #"s_dot = circle_create\((\d+), COL_ARC\)"#, "nowplaying.dot_px"),
        one(nv, #"lv_color_hex\("# + hex + #"\), 0\);\s+lv_label_set_text_static\(note"#, "nowplaying.colors.note"),
        one(nv, #"#define TITLE_W (\d+)"#, "nowplaying.title.width_px"),
        one(nv, #"#define SUB_W (\d+)"#, "nowplaying.sub.width_px"),
    ]
    for (key, def) in [("track", "TRACK"), ("arc", "ARC"), ("arc_paused", "ARC_PAUSED"), ("text", "TEXT"), ("sub", "SUB"),
                       ("meta", "META"), ("idle", "IDLE"), ("placeholder", "PLACEHOLDER")] {
        c.append(one(nv, "#define COL_\(def) " + hex, "nowplaying.colors.\(key)"))
    }
    for (key, obj, width) in [("title", "s_title", "TITLE_W"), ("sub", "s_sub", "SUB_W"), ("meta", "s_meta", "(\\d+)"),
                              ("idle", "s_idle", "(\\d+)")] {
        let widthKey = width.hasPrefix("(") ? [("nowplaying.\(key).width_px", g(2))] : []
        c.append(Check(file: nv, pattern: obj + #" = label_create\(&lv_font_montserrat_(\d+), "# + width + #", (-?\d+), "#,
                       keys: [("nowplaying.\(key).font_px", g(1)), ("nowplaying.\(key).dy_px", g(width.hasPrefix("(") ? 3 : 2))] + widthKey))
    }
    for (key, obj) in [("time", "s_time"), ("phase", "s_phase"), ("round", "s_round")] {
        c.append(Check(file: pv, pattern: obj + #" = label_create\(s_root, &lv_font_montserrat_(\d+), (\d+), (-?\d+)\)"#,
                       keys: [("pomodoro.\(key).font_px", g(1)), ("pomodoro.\(key).width_px", g(2)),
                              ("pomodoro.\(key).dy_px", g(3))]))
    }
    for (key, def) in [("sun", "SUN"), ("rays", "RAYS"), ("moon", "MOON"), ("star", "STAR"), ("cloud", "CLOUD"),
                       ("rain_cloud", "RAIN_CLOUD"), ("storm_cloud", "STORM_CLOUD"), ("lit_cloud", "LIT_CLOUD"),
                       ("snow_cloud", "SNOW_CLOUD"), ("rain", "RAIN"), ("snow", "SNOW"), ("bolt", "BOLT"),
                       ("fog", "FOG"), ("rime", "RIME")] {
        c.append(one(ws, "#define \(def)_RGB " + hex, "weather.colors.\(key)"))
    }
    for (key, spr) in [("rain", "RAIN"), ("flake_s", "FLAKE_S"), ("flake_l", "FLAKE_L"), ("cloud_l", "CLOUD_L"),
                       ("cloud_s", "CLOUD_S"), ("bolt", "BOLT"), ("moon", "MOON"), ("star", "STAR"), ("sun", "SUN"),
                       ("rays", "RAYS"), ("fog", "FOG")] {
        c.append(Check(file: ws, pattern: #"\[WX_SPR_"# + spr + #"\] = \{(\d+), (\d+), [\w\d]+\}"#,
                       keys: [("weather.scene.sprites.\(key)", arr(1, 2))]))
    }
    for (key, eye, angle) in [("dash", "DASH", #"L\((-?\d+), (-?\d+)\) \* lean"#), ("angry", "ANGRY", #"L\((-?\d+), (-?\d+)\) \* deg"#),
                              ("round", "ROUND", #"(\d+)() \* lean"#)] {
        c.append(Check(file: bs, pattern: "case BOT_EYES_\(eye):[^;]*?capsule\\(s, ex, ey, L\\(" + num + ", " + num
                         + #"\) \* ffx, L\("# + num + ", " + num + #"\) \* ffy, "# + angle,
                       keys: [("bot.eyes.\(key).width", arr(1, 2)), ("bot.eyes.\(key).height", arr(3, 4)),
                              ("bot.eyes.\(key).angle_deg", { c in "[" + n(c[5]) + "," + n(c[6].isEmpty ? c[5] : c[6]) + "]" })]))
    }
    return c
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func themeMatchesCinderSource() throws {
    let root = try #require(cinderDir).appendingPathComponent("firmware")
    var flat: [String: String] = [:]
    flatten(try themeJSON(), into: &flat)
    var covered = Set<String>()
    for check in allChecks() {
        let src = try String(contentsOf: root.appendingPathComponent(check.file), encoding: .utf8)
        let re = try NSRegularExpression(pattern: check.pattern, options: [.dotMatchesLineSeparators])
        guard let m = re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)) else {
            Issue.record("\(check.file): no match for \(check.pattern)")
            continue
        }
        let groups = (0..<m.numberOfRanges).map { i in
            Range(m.range(at: i), in: src).map { String(src[$0]) } ?? ""
        }
        for (key, value) in check.keys {
            covered.insert(key)
            #expect(flat[key] == value(groups), "\(key) vs \(check.file)")
        }
    }
    let codeOnly: Set<String> = ["version", "screen.diameter_px", "font.family", "font.file", "bot.body_color"]
    let uncovered = Set(flat.keys).subtracting(covered).subtracting(codeOnly)
        .filter { !$0.hasPrefix("font.metrics") }
    #expect(uncovered.isEmpty, "theme keys with no firmware check: \(uncovered.sorted())")
}

/// The preview's mark PNGs are the firmware's A8 masks (`tool_marks.c`), pixel for pixel.
@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func markImagesMatchFirmwareMasks() throws {
    let root = try #require(cinderDir).appendingPathComponent("firmware/components/bot/tool_marks.c")
    let src = try String(contentsOf: root, encoding: .utf8)
    for tool in ["claude", "codex"] {
        let re = try NSRegularExpression(pattern: "k_\(tool)\\[[^\\]]*\\] = \\{([^}]*)\\}", options: [.dotMatchesLineSeparators])
        let m = try #require(re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)))
        let body = String(src[Range(m.range(at: 1), in: src)!].drop(while: { $0 != "\n" }))   // past the size comment
        let dims = try #require(NSRegularExpression(pattern: "k_\(tool)\\[(\\d+) \\* (\\d+)\\]").firstMatch(in: src, range: NSRange(src.startIndex..., in: src)))
        let w = Int(src[Range(dims.range(at: 1), in: src)!])!, hh = Int(src[Range(dims.range(at: 2), in: src)!])!
        let want = body.split(whereSeparator: { $0 == "," || $0.isWhitespace }).compactMap { UInt8($0) }
        let url = try #require(Bundle.module.url(forResource: "knob-mark-\(tool)", withExtension: "png"))
        let img = try #require(CGImageSourceCreateWithURL(url as CFURL, nil).flatMap { CGImageSourceCreateImageAtIndex($0, 0, nil) })
        #expect(img.width == w && img.height == hh && want.count == w * hh, "\(tool) size")
        #expect(Double(img.height) == (try KnobTheme.load().bot.host.markHPx), "\(tool) height is the theme's mark height")
        var rgba = [UInt8](repeating: 0, count: img.width * img.height * 4)
        let cs = CGColorSpace(name: CGColorSpace.sRGB)!
        let ctx = CGContext(data: &rgba, width: img.width, height: img.height, bitsPerComponent: 8, bytesPerRow: img.width * 4,
                            space: cs, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(img, in: CGRect(x: 0, y: 0, width: img.width, height: img.height))
        let got = stride(from: 3, to: rgba.count, by: 4).map { rgba[$0] }
        #expect(got == want, "\(tool) mask differs from firmware")
    }
}
