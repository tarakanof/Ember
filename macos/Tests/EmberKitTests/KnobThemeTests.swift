import Foundation
import ImageIO
import Testing
@testable import EmberKit

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
    "font.metrics.36.line_height_px": "40",
    "font.metrics.36.baseline_px": "7",
    "font.metrics.48.baseline_px": "9",
    "font.metrics.96.line_height_px": "107",
    "font.metrics.96.baseline_px": "19",
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
    "bot.rim_px": "9",
    "bot.rim_dim_gain": "0.55",
    "bot.rim_steps": "5",
    "bot.ring_points": "192",
    "bot.triangle.radius": "1.1",
    "bot.triangle.sagitta": "0.11",
    "bot.triangle.table_bins": "720",
    "bot.triangle.blur_deg": "1.2",
    "bot.hop.duration_s": "1",
    "bot.hop.squash": "0.75",
    "bot.hop.interval_median_s": "2.8",
    "bot.hop.interval_sigma": "0.25",
    "bot.hop.interval_min_s": "2",
    "bot.hop.interval_max_s": "4.5",
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
    "bot.eyes.lid_keep": "0.3",
    "bot.eyes.lid_droop": "0.015",
    "bot.eyes.blink_hide": "0.9",
    "bot.eyes.dash.width": "0.14",
    "bot.eyes.dash.widen": "0.35",
    "bot.eyes.dash.height": "0.38",
    "bot.eyes.dash.angle_deg": "27",
    "bot.eyes.angry.width": "0.13",
    "bot.eyes.angry.widen": "0.35",
    "bot.eyes.angry.height": "0.3",
    "bot.eyes.angry.angle_deg": "55",
    "bot.eyes.round.width": "0.3",
    "bot.eyes.round.widen": "0.15",
    "bot.eyes.round.height": "0.44",
    "bot.eyes.round.angle_deg": "10",
    "bot.eyes.happy.width": "0.3",
    "bot.eyes.happy.height": "[0.16,0.03]",
    "bot.eyes.happy.lift": "1.5",
    "bot.eyes.happy.stroke": "0.11",
    "bot.eyes.happy.points": "16",
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
    "bot.host.sub_font_px": "18",
    "bot.host.sub_radius_px": "147",
    "bot.host.mark_bottom_sub_px": "128",
    "bot.host.canvas_w_px": "340",
    "bot.host.canvas_h_px": "116",
    "bot.host.canvas_y_px": "312",
    "bot.glint.width_px": "12",
    "bot.glint.tail_deg": "40",
    "bot.glint.white_mix": "0.85",
    "bot.glint.period_s": "3",
    "bot.glint.fps": "15",
    "bot.error.gaze_viewer_below": "0.45",
    "bot.error.gaze_down_below": "0.7",
    "bot.error.shake_at_s": "0.3",
    "bot.error.shake_s": "0.4",
    "bot.error.shake_cycles": "3",
    "bot.error.shake_amp": "0.0307",
    "bot.error.huff_s": "0.5",
    "bot.error.huff_dip": "0.03",
    "bot.error.huff_lid": "0.45",
    "bot.error.huff_median_s": "25",
    "bot.error.huff_sigma": "0.35",
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
    "weather.max_age_s": "1800",
    "weather.still_color": "#5A5A5A",
    "weather.scale.lo_c": "-20",
    "weather.scale.hi_c": "40",
    "weather.scale.stops_c": "[-20,-12,-6,-2,0,4,8,8.545455,9.090909,9.636364,10.181818,10.727273,11.272727,12,12.363636,12.909091,13.454545,14,15,25,35,40]",
    "weather.scale.colors": "[#B070E0,#7060E8,#4C84F0,#5CB0F0,#66C2EC,#52C6D2,#4AC5B9,#4BC8AA,#4CCAA2,#4CCB9A,#5CCC92,#6BCD8A,#7BCE82,#8ACF7A,#9AD072,#AAD06A,#BAD162,#C9D25A,#E8D44A,#F08A3A,#FF5050,#E0306A]",
    "weather.ring.radius_px": "222",
    "weather.ring.width_px": "12",
    "weather.ring.from_deg": "225",
    "weather.ring.sweep_deg": "270",
    "weather.ring.track_color": "#262626",
    "weather.marker.disc_radius_px": "17",
    "weather.marker.ring_radius_px": "10",
    "weather.marker.ring_width_px": "5",
    "weather.marker.color": "#F4F4F2",
    "weather.thermo.dy_px": "-14",
    "weather.thermo.head_y_px": "127",
    "weather.thermo.head_radius_px": "11",
    "weather.thermo.tube_mid_y_px": "147.875",
    "weather.thermo.tube_half_h_px": "20.875",
    "weather.thermo.tube_half_w_px": "11",
    "weather.thermo.bulb_y_px": "183",
    "weather.thermo.bulb_radius_px": "18",
    "weather.thermo.stroke_px": "4.5",
    "weather.thermo.mercury_bottom_px": "184",
    "weather.thermo.mercury_half_w_px": "4",
    "weather.thermo.mercury_bulb_radius_px": "10",
    "weather.thermo.mercury_top.ref_c": "12",
    "weather.thermo.mercury_top.ref_px": "144",
    "weather.thermo.mercury_top.rise_px": "22",
    "weather.thermo.mercury_top.per_c": "19",
    "weather.thermo.mercury_top.min_px": "122",
    "weather.thermo.mercury_top.max_px": "178",
    "weather.thermo.ticks_px": "[160,147,134]",
    "weather.thermo.tick_x_px": "[253,261]",
    "weather.thermo.tick_width_px": "3",
    "weather.thermo.tick_color": "#8A8A8A",
    "weather.value.font_px": "96",
    "weather.value.y_px": "200",
    "weather.value.letter_space_px": "-2",
    "weather.ends.font_px": "36",
    "weather.ends.x_px": "96",
    "weather.ends.y_px": "362",
    "weather.sub.font_px": "30",
    "weather.sub.y_px": "302",
    "weather.sub.color": "#BDBDBD",
    "weather.dots.radius_px": "206",
    "weather.dots.pitch_px": "16",
    "weather.dots.dot_radius_px": "4",
    "weather.dots.on_color": "#FFFFFF",
    "weather.dots.off_color": "#4A4A4A",
    "weather.dots.faces": "7",
    "weather.colors.cloud": "#9AA4B0",
    "weather.colors.rain": "#5C9CE0",
    "weather.colors.snow": "#D0DCE8",
    "weather.colors.bolt": "#F0D040",
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
    let scale = try KnobTheme.load().weather.scale
    #expect(scale.stopsC.count == scale.colors.count)
    #expect(scale.stopsC == scale.stopsC.sorted() && scale.stopsC.first == scale.loC && scale.stopsC.last == scale.hiC)
    #expect(KnobTheme.fontURL != nil)
}

@Test func knobThemeRejectsMissingKeysAndBadColours() {
    #expect(throws: (any Error).self) { try KnobTheme.decode(Data(#"{"version":1}"#.utf8)) }
    #expect(throws: (any Error).self) { try JSONDecoder().decode([RGB].self, from: Data(#"["red"]"#.utf8)) }
    #expect((try? JSONDecoder().decode([RGB].self, from: Data(##"["#0A0B0C"]"##.utf8))) == [RGB(r: 10, g: 11, b: 12)])
}

@Test func cinderDirFromTheEnvironmentHasEveryFileTheParityTestsRead() {
    guard let env = ProcessInfo.processInfo.environment["CINDER_DIR"] else { return }
    let root = URL(fileURLWithPath: env)
    for file in ["firmware/main/bot_view.c", "firmware/components/ember_host/include/ember_host.h", "firmware/components/bot/tool_marks.c", "firmware/components/nowplaying/np.c", "firmware/test/host/test_np.c",
                 "firmware/components/bot/bot_behavior.c",
                 "firmware/main/temp_face.c", "firmware/main/font_digits_96.c", "firmware/main/font_face_36.c",
                 "firmware/components/weather/wx_temp.c", "firmware/components/weather/wx_paint.c",
                 "firmware/components/weather/include/wx_temp.h", "firmware/components/weather/include/wx_paint.h",
                 "firmware/components/weather/include/wx_faces.h", "firmware/components/weather/include/wx_cast.h",
                 "firmware/components/weather/include/wx_wind.h"] {
        #expect(FileManager.default.fileExists(atPath: root.appendingPathComponent(file).path), "CINDER_DIR has no \(file)")
    }
}

private let cinderDir: URL? = {
    let fm = FileManager.default
    var candidates: [URL] = []
    if let env = ProcessInfo.processInfo.environment["CINDER_DIR"] { candidates.append(URL(fileURLWithPath: env)) }
    var root = URL(fileURLWithPath: #filePath)
    for _ in 0..<4 { root.deleteLastPathComponent() }
    candidates.append(root.deletingLastPathComponent().appendingPathComponent("cinder"))
    return candidates.first { fm.fileExists(atPath: $0.appendingPathComponent("firmware/main/bot_view.c").path) }
}()

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
private let bbh = "components/bot/include/bot_behavior.h", bsh = "components/bot/include/bot_shape.h"
private let nv = "main/nowplaying_view.c", nc = "main/nowplaying_client.c", nh = "components/nowplaying/include/np.h"
private let pv = "main/pomo_view.c", tf = "main/temp_face.c"
private let wt = "components/weather/wx_temp.c", wth = "components/weather/include/wx_temp.h"
private let wp = "components/weather/wx_paint.c", wph = "components/weather/include/wx_paint.h"
private let wfh = "components/weather/include/wx_faces.h", wch = "components/weather/include/wx_cast.h"
private let wwh = "components/weather/include/wx_wind.h"
private func twice(_ i: Int) -> ([String]) -> String { { n(String(2 * (Double(n($0[i])) ?? .nan))) } }
private func pairs(_ re: String, _ i: Int) -> ([String]) -> String {
    { c in
        let body = c[1], rx = try! NSRegularExpression(pattern: re)
        let ms = rx.matches(in: body, range: NSRange(body.startIndex..., in: body))
        return "[" + ms.map { n(String(body[Range($0.range(at: i), in: body)!])) }.joined(separator: ",") + "]"
    }
}

private func allChecks() -> [Check] {
    var c: [Check] = [
        one(bv, #"#define FILL "# + num, "bot.fill"),
        one(bv, #"#define HOP_SCALE "# + num, "bot.hop_scale"),
        one("main/bot_view.h", #"#define BOT_VIEW_EYE_SCALE "# + num, "bot.eye_scale"),
        one(bv, #"#define RIM_PX "# + num, "bot.rim_px"),
        one("components/bot/include/bot_behavior.h", #"#define BOT_RIM_STEPS (\d+)"#, "bot.rim_steps"),
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
        one(bb, #"m == BOT_IDLE \|\| m == BOT_SLEEPY \? "# + num, "bot.rim_dim_gain"),
        one(bv, hex + #", 1\.0f\);"#, "bot.eye_color"),
        one(bv, #"arc_line\(&layer, &lv_font_montserrat_(\d+), s_host\.text, LABEL_R, "#, "bot.host.font_px"),
        one(bv, #"arc_line\(&layer, &lv_font_montserrat_(\d+), sub, LABEL_SUB_R, "#, "bot.host.sub_font_px"),
        one(bv, #"#define LABEL_SUB_R "# + num, "bot.host.sub_radius_px"),
        one(bv, #"#define LABEL_ICON_SUB_R (\d+)"#, "bot.host.mark_bottom_sub_px"),
        one("main/bot_view.h", #"#define BOT_VIEW_LABEL_W (\d+)"#, "bot.host.canvas_w_px"),
        one("main/bot_view.h", #"#define BOT_VIEW_LABEL_H (\d+)"#, "bot.host.canvas_h_px"),
        one("main/bot_view.h", #"#define BOT_VIEW_LABEL_Y (\d+)"#, "bot.host.canvas_y_px"),
        Check(file: bv, pattern: #"#define LABEL_X \(\(int\)CX - LABEL_W / 2\)"#, keys: []),
        Check(file: bv, pattern: #"y = \(int\)\(CY - LABEL_Y\) \+ \(sub\[0\] \? LABEL_ICON_SUB_R : LABEL_ICON_R\);"#, keys: []),
        Check(file: bv, pattern: #"bool marked = bot_label_mark\(s_rim_mood\);"#, keys: []),
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
        one(bs, #"BOT_EYES_DASH \? "# + num + #" \* side \* lean \* sg"#, "bot.eyes.rise"),
        Check(file: bs, pattern: #"double sg = cx < 0 \? -1 : 1;"#, keys: []),
        one(bsh, #"#define BOT_LID_KEEP "# + num, "bot.eyes.lid_keep"),
        one(bsh, #"#define BOT_LID_DROOP "# + num, "bot.eyes.lid_droop"),
        one(bbh, #"#define BOT_BLINK_HIDE "# + num, "bot.eyes.blink_hide"),
        Check(file: bs, pattern: #"droop = p->eyes == BOT_EYES_HAPPY \? 0 : BOT_LID_DROOP \* lc;"#, keys: []),
        Check(file: bs, pattern: #"s->lid = p->eyes == BOT_EYES_HAPPY \? \(hide >= 1 \? 1 : 0\) : lc;\s+"#
                + #"s->keep = p->eyes == BOT_EYES_HAPPY \? 0 : BOT_LID_KEEP \* \(1 - hide\);"#, keys: []),
        Check(file: bs, pattern: #"top -= width / 2 \+ 1;\s+bot \+= width / 2 \+ 1;\s+"#
                + #"return top \+ \(bot - keep \* width - top\) \* \(lid > 1 \? 1 : lid\);"#, keys: []),
        Check(file: bb, pattern: #"case BOT_ERROR: \{\s+if \(r < "# + num
                + #"\) \{ \*x = VIEWER_X \+ jitter\(b, 0\.08\); \*y = VIEWER_Y \+ jitter\(b, 0\.08\); \*fix = lognormal\(b, 1\.7, 0\.3, 1\.2, 2\.5\); return; \}\s+if \(r < "#
                + num + #"\) \{ \*x = jitter\(b, 0\.15\); \*y = -0\.5 \+ jitter\(b, 0\.1\);[^\n]*\s+"#
                + #"double side = [^\n]*\s+\*x = side \* rng_range\(b, 0\.7, 0\.9\); \*y = jitter\(b, 0\.3\); \*fix = rng_range\(b, 0\.4, 0\.9\);"#,
              keys: [("bot.error.gaze_viewer_below", g(1)), ("bot.error.gaze_down_below", g(2))]),
        Check(file: bb, pattern: #"#define LID_SHUT BOT_BLINK_HIDE"#, keys: []),
        Check(file: bb, pattern: #"double u = t - b->blink_start, shut = 0\.075 \* b->blink_speed \+ fmax\(b->blink_lag, 0\);"#, keys: []),
        Check(file: bb, pattern: #"if \(b->swap_eyes_on_close && u <= \(b->blink_heavy \? [^:]+ : shut\)\) b->blink_swap = true;\s+"#
                + #"if \(b->swap_eyes_on_close && b->blink_swap && fmin\(bl, br\) >= LID_SHUT && now\) swap_eyes\(b, t\);"#, keys: []),
        Check(file: bb, pattern: #"\} else if \(\(beat_due \|\| b->swap_eyes_on_close\) && !b->asleep\) \{\s+reclose\(b, t\);"#, keys: []),
        Check(file: bb, pattern: #"static void reclose\(bot_t \*b, double t\)\s*\{[^}]*b->blink_lag = 0;[^}]*"#
                + #"if \(b->blink_speed < 1\.6\) b->blink_speed = 1\.6;"#, keys: []),
        Check(file: bb, pattern: #"b->shake_at = t \+ BOT_SHAKE_AT_S;\s+b->next_huff_at = b->shake_at \+ huff_gap\(b\);"#, keys: []),
        Check(file: bb, pattern: #"if \(fx && !b->acting\) b->huff_at = t;\s+b->next_huff_at = t \+ huff_gap\(b\);"#, keys: []),
        Check(file: bb, pattern: #"if \(\(!fx && t < b->shake_at\) \|\| b->tracking\) b->shake_at = -INFINITY;"#, keys: []),
        Check(file: bb, pattern: #"if \(t < b->shake_at\) b->shake_at = -INFINITY;\s+b->next_huff_at = INFINITY;"#, keys: []),
        one(bbh, #"#define BOT_SHAKE_AT_S "# + num, "bot.error.shake_at_s"),
        one(bbh, #"#define BOT_SHAKE_S "# + num, "bot.error.shake_s"),
        one(bbh, #"#define BOT_SHAKE_CYCLES "# + num, "bot.error.shake_cycles"),
        one(bbh, #"#define BOT_SHAKE_AMP "# + num, "bot.error.shake_amp"),
        one(bbh, #"#define BOT_HUFF_S "# + num, "bot.error.huff_s"),
        one(bbh, #"#define BOT_HUFF_DIP "# + num, "bot.error.huff_dip"),
        one(bbh, #"#define BOT_HUFF_LID "# + num, "bot.error.huff_lid"),
        one(bbh, #"#define BOT_HUFF_MEDIAN_S "# + num, "bot.error.huff_median_s"),
        one(bb, #"lognormal\(b, BOT_HUFF_MEDIAN_S, "# + num + #", BOT_HUFF_MEDIAN_S / 2, BOT_HUFF_MEDIAN_S \* 2\)"#,
            "bot.error.huff_sigma"),
        Check(file: bb, pattern: #"p\.offset_x \+= BOT_SHAKE_AMP \* sin\(2 \* M_PI \* BOT_SHAKE_CYCLES \* su\) \* \(1 - su\);"#
                + #"\s+if \(huffing\) \{\s+double k = sin\(M_PI \* hu\);\s+p\.eye_dy = -BOT_HUFF_DIP \* k;"#, keys: []),
        one(bs, #"if \(half < "# + num, "bot.eyes.min_half"),
        Check(file: bs, pattern: #"double w = "# + num + #" \* ffx, h = L\("# + num + ", " + num + #"\) \* fh"#,
              keys: [("bot.eyes.happy.width", g(1)), ("bot.eyes.happy.height", arr(2, 3))]),
        one(bs, #"qy = ey \+ h \* "# + num, "bot.eyes.happy.lift"),
        Check(file: bs, pattern: #"case BOT_EYES_HAPPY:[^}]*?s->n = BOT_STROKE_MAX;"#, keys: []),
        one("components/bot/include/bot_shape.h", #"#define BOT_STROKE_MAX (\d+)"#, "bot.eyes.happy.points"),
        one(bs, #"s->width = "# + num + #" \* fmin"#, "bot.eyes.happy.stroke"),
        Check(file: bb, pattern: #"if \(ph == 1\) \{(?:(?!\} else)[\s\S])*?next_hop_at = t \+ lognormal\(b, "# + num + ", " + num + ", " + num + ", " + num,
              keys: [("bot.hop.interval_median_s", g(1)), ("bot.hop.interval_sigma", g(2)),
                     ("bot.hop.interval_min_s", g(3)), ("bot.hop.interval_max_s", g(4))]),
        one("components/bot/include/bot_behavior.h", #"#define BOT_HOP_DURATION_S "# + num, "bot.hop.duration_s"),
        one("components/bot/include/bot_behavior.h", #"#define BOT_HOP_SQUASH "# + num, "bot.hop.squash"),
        one("components/ember_host/include/ember_host.h", #"#define EMBER_HOST_MAX (\d+)"#, "bot.host.max_chars"),
        one(pv, #"#define TRACK_GAIN "# + num, "pomodoro.track_gain"),
        one(pv, #"#define PAUSED_GAIN "# + num, "pomodoro.paused_gain"),
        Check(file: pv, pattern: #"\.r = (\d+), \.hw = (\d+)"#,
              keys: [("pomodoro.ring_radius_px", g(1)), ("pomodoro.ring_width_px", { n(String(2 * (Int($0[2]) ?? 0))) })]),
        one(tf, #"#define GREY_RGB "# + hex, "weather.still_color"),
        one("components/weather/include/weather_face.h", #"#define WX_MAX_AGE_S "# + num, "weather.max_age_s"),
        one(wth, #"#define WX_SCALE_LO \((-?\d+)\)"#, "weather.scale.lo_c"),
        one(wth, #"#define WX_SCALE_HI (\d+)"#, "weather.scale.hi_c"),
        Check(file: wt, pattern: #"static const stop_t SCALE\[\] = \{(.*?)\};"#,
              keys: [("weather.scale.stops_c", pairs(#"\{(-?[\d.]+f?), (0x[0-9A-Fa-f]{6})\}"#, 1)),
                     ("weather.scale.colors", pairs(#"\{(-?[\d.]+f?), (0x[0-9A-Fa-f]{6})\}"#, 2))]),
        Check(file: wt, pattern: #"uint32_t wx_temp_rgb\(float c\)\s*\{\s*if \(!\(c > SCALE\[0\]\.t\)\) return SCALE\[0\]\.rgb;\s*"#
                + #"if \(c >= SCALE\[N_SCALE - 1\]\.t\) return SCALE\[N_SCALE - 1\]\.rgb;[^}]*?"#
                + #"int v = \(int\)lroundf\(ca \+ \(cb - ca\) \* f\);"#, keys: []),
        one(wth, #"#define WX_RING_R "# + num, "weather.ring.radius_px"),
        Check(file: wth, pattern: #"#define WX_RING_HW "# + num, keys: [("weather.ring.width_px", twice(1))]),
        Check(file: wt, pattern: #"float a = \("# + num + #" \+ "# + num + #" \* frac\) \* \(float\)M_PI / 180\.0f;\s+"#
                + #"\*x = WX_FACE_C \+ WX_RING_R \* sinf\(a\);\s+\*y = WX_FACE_C - WX_RING_R \* cosf\(a\);"#,
              keys: [("weather.ring.from_deg", g(1)), ("weather.ring.sweep_deg", g(2))]),
        one(wp, #"lut\[i\] = wx_rgb565\(grey \? "# + hex + #" : wx_temp_rgb\("#, "weather.ring.track_color"),
        Check(file: wt, pattern: #"float f = \(float\)\(t->value - t->lo\) / \(float\)\(t->hi - t->lo\);\s+"#
                + #"t->frac = f < 0 \? 0 : f > 1 \? 1 : f;"#, keys: []),
        Check(file: wt, pattern: #"t->lo = WX_SCALE_LO;\s+t->hi = WX_SCALE_HI;\s+t->sub_rgb = "# + hex,
              keys: [("weather.sub.color", g(1))]),
        Check(file: wt, pattern: #"t->lo = o->min_c < t->value \? o->min_c : t->value;\s+"#
                + #"t->hi = o->max_c > t->value \? o->max_c : t->value;\s+if \(t->lo == t->hi\) \{\s+t->lo--;\s+t->hi\+\+;"#,
              keys: []),
        Check(file: wp, pattern: #"put\(im, i, 0, cov\(d - "# + num + #"\)\);\s+put\(im, i, ring, cov\(fabsf\(d - "# + num
                + #"\) - "# + num + #"\)\);"#,
              keys: [("weather.marker.disc_radius_px", g(1)), ("weather.marker.ring_radius_px", g(2)),
                     ("weather.marker.ring_width_px", twice(3))]),
        one(wp, #"const uint16_t ring = wx_rgb565\("# + hex + #"\);"#, "weather.marker.color"),
        one(wth, #"#define WX_THERMO_DY \((-?[\d.]+f?)\)"#, "weather.thermo.dy_px"),
        Check(file: wp, pattern: #"float top = hypotf\(x - 233, y - \("# + num + #" \+ dy\)\) - "# + num + #";\s+"#
                + #"float bulb = hypotf\(x - 233, y - \("# + num + #" \+ dy\)\) - "# + num + #";\s+"#
                + #"float qx = fabsf\(x - 233\) - "# + num + #", qy = fabsf\(y - \("# + num + #" \+ dy\)\) - "# + num + ";",
              keys: [("weather.thermo.head_y_px", g(1)), ("weather.thermo.head_radius_px", g(2)),
                     ("weather.thermo.bulb_y_px", g(3)), ("weather.thermo.bulb_radius_px", g(4)),
                     ("weather.thermo.tube_half_w_px", g(5)), ("weather.thermo.tube_mid_y_px", g(6)),
                     ("weather.thermo.tube_half_h_px", g(7))]),
        Check(file: wp, pattern: #"const uint16_t stroke = wx_rgb565\("# + hex + #"\);[^}]*?"#
                + #"put\(im, y \* im->w \+ x, 0, cov\(d\)\);\s+put\(im, y \* im->w \+ x, stroke, cov\(fabsf\(d\) - "# + num + #"\)\);"#,
              keys: [("weather.colors.cloud", g(1)), ("weather.thermo.stroke_px", twice(2))]),
        one(wth, #"#define WX_MERC_BOTTOM "# + num, "weather.thermo.mercury_bottom_px"),
        Check(file: wp, pattern: #"static const float TICKS\[3\] = \{([^}]*)\};"#, keys: [("weather.thermo.ticks_px", list(1))]),
        one(wp, #"grey = wx_rgb565\("# + hex + #"\);\s+im->x0 = WX_THERMO_X;"#, "weather.thermo.tick_color"),
        Check(file: wp, pattern: #"qy = fabsf\(fy - cy\) - \(hh - "# + num + #" > 0 \? hh - \1 : 0\);\s+"#
                + #"float rr = \(qy > 0 \? hypotf\(qx, qy\) : qx\) - \1;\s+"#
                + #"float bulb = hypotf\(fx - 233, fy - \(183 \+ dy\)\) - "# + num + #";"#,
              keys: [("weather.thermo.mercury_half_w_px", g(1)), ("weather.thermo.mercury_bulb_radius_px", g(2))]),
        Check(file: wp, pattern: #"put\(im, i, top <= TICKS\[t\] \? c : grey, cov\(capsule\(fx, fy, "# + num + ", " + num
                + #", TICKS\[t\] \+ dy\) - "# + num + #"\)\);"#,
              keys: [("weather.thermo.tick_x_px", arr(1, 2)), ("weather.thermo.tick_width_px", twice(3))]),
        Check(file: wp, pattern: #"float cy = \(top \+ WX_MERC_BOTTOM\) / 2 \+ dy, hh = \(WX_MERC_BOTTOM - top\) / 2;"#, keys: []),
        Check(file: wt, pattern: #"float top = "# + num + #" - \(c - "# + num + #"\) \* "# + num + " / " + num + #";\s+"#
                + #"return top < "# + num + #" \? \5 : top > "# + num + #" \? \6 : top;"#,
              keys: [("weather.thermo.mercury_top.ref_px", g(1)), ("weather.thermo.mercury_top.ref_c", g(2)),
                     ("weather.thermo.mercury_top.rise_px", g(3)), ("weather.thermo.mercury_top.per_c", g(4)),
                     ("weather.thermo.mercury_top.min_px", g(5)), ("weather.thermo.mercury_top.max_px", g(6))]),
        Check(file: tf, pattern: #"uint32_t rgb = t->still \? GREY_RGB : wx_temp_rgb\(\(float\)v\);\s+deg_text\(txt, sizeof txt, v\);\s+"#
                + #"set_value\(txt, rgb\);\s+float top = WX_MERC_BOTTOM - \(WX_MERC_BOTTOM - wx_merc_top\(\(float\)t->value\)\) \* \(float\)a;\s+"#
                + #"set_mercury\(top, rgb\);\s+\} else \{\s+set_value\("--\\xC2\\xB0", GREY_RGB\);\s+set_mercury\(WX_MERC_BOTTOM, GREY_RGB\);"#,
              keys: []),
        one(tf, #"#define VALUE_Y (\d+)"#, "weather.value.y_px"),
        one(tf, #"#define VALUE_SPACE \((-?\d+)\)"#, "weather.value.letter_space_px"),
        one(tf, #"s_value = label\(s_root, &font_digits_(\d+), LV_ALIGN_TOP_MID, deg / 2, VALUE_Y\);"#, "weather.value.font_px"),
        one(tf, #"#define END_X (\d+)"#, "weather.ends.x_px"),
        one(tf, #"#define END_Y (\d+)"#, "weather.ends.y_px"),
        one(tf, #"s_lo = label\(s_root, &font_face_(\d+), LV_ALIGN_TOP_LEFT, END_X, END_Y\);\s+"#
            + #"s_hi = label\(s_root, &font_face_\1, LV_ALIGN_TOP_RIGHT, -END_X, END_Y\);"#, "weather.ends.font_px"),
        Check(file: tf, pattern: #"end_label\(s_lo, s_lo_txt, sizeof s_lo_txt, t->have && t->has_range, t->lo\);\s+"#
                + #"end_label\(s_hi, s_hi_txt, sizeof s_hi_txt, t->have && t->has_range, t->hi\);"#, keys: []),
        one(tf, #"s_sub = label\(s_root, &lv_font_montserrat_(\d+), LV_ALIGN_TOP_MID, 0, SUB_Y\);"#, "weather.sub.font_px"),
        one(wch, #"#define WX_SUB_Y (\d+)"#, "weather.sub.y_px"),
        one(wfh, #"#define WXF_DOT_R "# + num, "weather.dots.radius_px"),
        one(wfh, #"#define WXF_DOT_PITCH "# + num, "weather.dots.pitch_px"),
        one(wph, #"#define WX_DOT_R "# + num, "weather.dots.dot_radius_px"),
        one(wph, #"#define WX_DOT_ON "# + hex, "weather.dots.on_color"),
        one(wph, #"#define WX_DOT_OFF "# + hex, "weather.dots.off_color"),
        Check(file: wfh, pattern: #"typedef enum \{ (WXF_TEMP(?:, WXF_[A-Z]+)*), WXF_N \} wx_face_id_t;"#,
              keys: [("weather.dots.faces", { String($0[1].split(separator: ",").count) })]),
        Check(file: wp, pattern: #"wx_paint_thermo\(&bg\);\s+wx_paint_face_dots\(&bg, t->face, WXF_N\);"#, keys: []),
        one(tf, #"#define SNOW_RGB "# + hex, "weather.colors.snow"),
        one(tf, #"#define BOLT_RGB "# + hex, "weather.colors.bolt"),
        one(wch, #"#define WX_CAST_RGB "# + hex, "weather.colors.rain"),
        one(wwh, #"#define WX_WIND_POLE_RGB "# + hex, "weather.colors.cloud"),
    ]
    for (px, file) in [("96", "main/font_digits_96.c"), ("36", "main/font_face_36.c")] {
        c.append(Check(file: file, pattern: #"\.line_height = (\d+),\s+\.base_line = (\d+),"#,
                       keys: [("font.metrics.\(px).line_height_px", g(1)), ("font.metrics.\(px).baseline_px", g(2))]))
    }
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
    for (key, eye, angle) in [("dash", "DASH", #"(\d+) \* lean \* sg \* deg"#), ("angry", "ANGRY", #"(\d+) \* deg \* -side"#),
                              ("round", "ROUND", #"(\d+) \* lean \* sg \* deg"#)] {
        c.append(Check(file: bs, pattern: "case BOT_EYES_\(eye):[^;]*?capsule\\(s, ex, ey, " + num + #" \* \(1 \+ "# + num
                         + #" \* lc\) \* ffx, "# + num + #" \* fh, "# + angle,
                       keys: [("bot.eyes.\(key).width", g(1)), ("bot.eyes.\(key).widen", g(2)),
                              ("bot.eyes.\(key).height", g(3)), ("bot.eyes.\(key).angle_deg", g(4))]))
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
        .filter { !$0.hasPrefix("font.metrics") || $0.hasPrefix("font.metrics.36.") || $0.hasPrefix("font.metrics.96.") }
    #expect(uncovered.isEmpty, "theme keys with no firmware check: \(uncovered.sorted())")
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func markImagesMatchFirmwareMasks() throws {
    let root = try #require(cinderDir).appendingPathComponent("firmware/components/bot/tool_marks.c")
    let src = try String(contentsOf: root, encoding: .utf8)
    for tool in ["claude", "codex"] {
        let re = try NSRegularExpression(pattern: "k_\(tool)\\[[^\\]]*\\] = \\{([^}]*)\\}", options: [.dotMatchesLineSeparators])
        let m = try #require(re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)))
        let body = String(src[Range(m.range(at: 1), in: src)!].drop(while: { $0 != "\n" }))
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

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func projectSlotsMatchCinder() throws {
    let h = try #require(cinderDir).appendingPathComponent("firmware/components/ember_host/include/ember_host.h")
    let src = try String(contentsOf: h, encoding: .utf8)
    for (name, want) in [("EMBER_PROJECT_SLOTS", KnobMood.projectSlots), ("EMBER_LABEL_SLOTS", KnobMood.labelSlots)] {
        let re = try NSRegularExpression(pattern: "#define \(name) (\\d+)")
        let m = try #require(re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)), "\(name)")
        #expect(Int(src[Range(m.range(at: 1), in: src)!]) == want, "\(name)")
    }
    #expect(src.contains(#"#define EMBER_ELLIPSIS "...""#))
}

private func cinderMoods(_ expr: String, _ term: String) throws -> Set<BotMood> {
    let re = try NSRegularExpression(pattern: term + "BOT_([A-Z]+)")
    let found = re.matches(in: expr, range: NSRange(expr.startIndex..., in: expr)).map { m in
        BotMood(rawValue: String(expr[Range(m.range(at: 1), in: expr)!]).lowercased())
    }
    #expect(!found.contains(nil), "unknown mood in \(expr)")
    let rest = re.stringByReplacingMatches(in: expr, range: NSRange(expr.startIndex..., in: expr), withTemplate: "")
    let plain = rest.replacingOccurrences(of: "||", with: "").allSatisfy { $0.isWhitespace }
    #expect(plain, "not a plain mood list: \(expr)")
    return Set(found.compactMap { $0 })
}

private func cinderMatch(_ file: String, _ pattern: String) throws -> String {
    let src = try String(contentsOf: try #require(cinderDir).appendingPathComponent("firmware/" + file), encoding: .utf8)
    let re = try NSRegularExpression(pattern: pattern, options: [.dotMatchesLineSeparators])
    let m = try #require(re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)), "\(file): no match for \(pattern)")
    return String(src[Range(m.range(at: 1), in: src)!])
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func toolMarkGateMatchesCinder() throws {
    let hidden = try cinderMoods(try cinderMatch(bb, #"bool bot_label_mark\(bot_mood_t m\) \{ return (m != BOT_[A-Z]+); \}"#), "m != ")
    #expect(Set(BotMood.allCases.filter { !KnobMood.marksTool($0) }) == hidden)
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func labelMoodsAndHostLineMatchCinder() throws {
    let show = try cinderMatch(bv, #"static void host_sync\(void\)\s*\{\s*bool show = s_opt_label && s_host\.text\[0\] &&\s*\(([^;]*)\);"#)
    let shown = try cinderMoods(show, "s_rim_mood == ")
    #expect(shown == KnobMood.labelMoods)
    let sub = try cinderMatch(bv, #"const char \*sub = ([^?;]*) \? s_host\.sub : "";"#)
    let lined = try cinderMoods(sub, "s_rim_mood == ")
    #expect(lined == KnobMood.hostLineMoods)
}
