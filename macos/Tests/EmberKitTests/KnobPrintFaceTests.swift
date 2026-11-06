import Foundation
import ImageIO
import SwiftUI
import Testing
@testable import EmberKit

private let c = KnobTheme.standard.print.colors
private let now = Date(timeIntervalSince1970: 1_800_000_000)
private let t0 = Int64(now.timeIntervalSince1970)
private let utc = TimeZone(secondsFromGMT: 0)!

private func face(_ b: KnobPrintBlock?, at: Date = now, zone: TimeZone? = utc, link: KnobPrintFace.Link = .fed,
                  doneSince: Date? = nil) -> KnobPrintFace {
    KnobPrintFace(block: b, now: at, zone: zone, link: link, doneSince: doneSince)
}

private func block(_ phase: KnobPrintBlock.Phase, progress: Int? = 46, endsAt: Int64? = nil, startedAt: Int64? = nil,
                   changeAt: Int64? = nil, temps: KnobPrintBlock.Temps? = nil, event: KnobPrintBlock.Event? = nil) -> KnobPrintBlock {
    KnobPrintBlock(phase: phase, progress: progress, endsAt: endsAt, startedAt: startedAt, changeAt: changeAt, temps: temps,
                   name: "Desk lamp", material: "PLA", printer: "CORE One", event: event)
}

@Test func blockMirrorsTheServersKnobBlock() {
    #expect(KnobPrintBlock(state: nil) == nil)
    #expect(KnobPrintBlock(state: PrinterState(enabled: false, phase: "printing")) == nil)
    #expect(KnobPrintBlock(state: PrinterState(enabled: true, phase: "off")) == nil)

    let info = PrinterInfo(name: String(repeating: "Ж", count: 40))
    let idle = KnobPrintBlock(state: PrinterState(enabled: true, phase: "idle", printer: info, progress: 0,
                                                  event: PrinterEvent(id: 7, kind: "finished", at: 10), nozzle: 215,
                                                  nozzleTarget: 0, bed: 62, bedTarget: 0, name: "x"))
    #expect(idle == KnobPrintBlock(phase: .idle, printer: String(repeating: "Ж", count: 32), event: .init(kind: "finished", at: 10)))

    let heating = KnobPrintBlock(state: PrinterState(enabled: true, phase: "heating", progress: 0, endsAt: t0 + 60,
                                                     nozzle: 182, nozzleTarget: 240, bed: 72, bedTarget: 85,
                                                     name: "Lamp", material: "PLA/PETG plus a long tail"))
    #expect(heating?.temps == .init(nozzle: 182, nozzleTarget: 240, bed: 72, bedTarget: 85))
    #expect(heating?.material == "PLA/PETG plus a ")
    #expect(heating?.endsAt == t0 + 60)

    let printing = KnobPrintBlock(state: PrinterState(enabled: true, phase: "printing", progress: 101, endsAt: 0, nozzle: 215,
                                                      nozzleTarget: 215, bed: 60, bedTarget: 60))
    #expect(printing?.temps == nil && printing?.progress == nil && printing?.endsAt == nil)
    #expect(KnobPrintBlock(state: PrinterState(enabled: true, phase: "busy"))?.phase == .unknown)
    let marks = String(repeating: "e\u{301}", count: 10)
    let cut = KnobPrintBlock(state: PrinterState(enabled: true, phase: "printing", progress: 1, material: marks))?.material
    #expect(cut?.unicodeScalars.count == 16 && cut == String(repeating: "e\u{301}", count: 8), "16 runes, as Go's firstRunes")
    #expect(KnobPrintBlock(state: PrinterState(enabled: true, phase: "heating", nozzle: 600, nozzleTarget: 240, bed: 72,
                                               bedTarget: 85))?.temps == nil)
}

@Test func printerStateDecodesTheJobFields() throws {
    let json = #"{"enabled":true,"phase":"printing","progress":42,"ends_at":1800006120,"started_at":1799990000,"change_at":1800000300,"nozzle":215,"nozzle_target":215,"bed":60,"bed_target":60,"name":"Desk lamp","material":"PLA","events":[],"printer":{"name":"CORE One"}}"#
    let s = try JSONDecoder().decode(PrinterState.self, from: Data(json.utf8))
    #expect(s.endsAt == 1_800_006_120 && s.startedAt == 1_799_990_000 && s.changeAt == 1_800_000_300)
    #expect(s.nozzle == 215 && s.nozzleTarget == 215 && s.bed == 60 && s.bedTarget == 60)
    #expect(s.name == "Desk lamp" && s.material == "PLA" && s.printer.name == "CORE One")
    let bare = try JSONDecoder().decode(PrinterState.self, from: Data(#"{"enabled":true,"phase":"idle","ends_at":"x"}"#.utf8))
    #expect(bare.endsAt == nil && bare.name.isEmpty)
}

private struct Row {
    let phase: KnobPrintBlock.Phase?
    let look: KnobPrintFace.Look
    let ring: RGB
    let head: Bool
    let pulses: Bool
    let frac: Double
    let word: String
    let wordColor: RGB?
    let sub: String
    let subColor: RGB?
}

@Test func everyPhaseMatchesCindersTable() {
    let black = RGB(r: 0, g: 0, b: 0)
    let half = KnobPrintFace.mix(black, c.accent, 0.5)
    let rows: [Row] = [
        .init(phase: nil, look: .none, ring: c.track, head: false, pulses: false, frac: 0, word: "Printer off", wordColor: c.dimWord,
              sub: "", subColor: nil),
        .init(phase: .offline, look: .offline, ring: c.track, head: false, pulses: false, frac: 0, word: "Printer off",
              wordColor: c.dimWord, sub: "CORE One", subColor: c.dimWord),
        .init(phase: .idle, look: .idle, ring: c.track, head: false, pulses: false, frac: 0, word: "Ready", wordColor: c.text,
              sub: "CORE One", subColor: c.small),
        .init(phase: .unknown, look: .idle, ring: c.track, head: false, pulses: false, frac: 0, word: "Idle", wordColor: c.sub,
              sub: "CORE One", subColor: c.small),
        .init(phase: .finishing, look: .finishing, ring: c.accent, head: true, pulses: false, frac: 1, word: "", wordColor: nil,
              sub: "finishing up", subColor: c.accent),
        .init(phase: .paused, look: .paused, ring: half, head: false, pulses: false, frac: 0.46, word: "Paused", wordColor: c.wait,
              sub: "at 46%", subColor: c.sub),
        .init(phase: .attention, look: .attention, ring: c.wait, head: true, pulses: true, frac: 0.46, word: "Check printer",
              wordColor: c.wait, sub: "paused \u{2022} 46% done", subColor: c.sub),
        .init(phase: .finished, look: .done, ring: c.done, head: false, pulses: false, frac: 1, word: "Done", wordColor: c.done,
              sub: "take it out", subColor: c.sub),
        .init(phase: .stopped, look: .stopped, ring: c.grey, head: false, pulses: false, frac: 0.46, word: "Stopped",
              wordColor: c.sub, sub: "at 46%", subColor: c.sub),
        .init(phase: .error, look: .error, ring: c.err, head: false, pulses: true, frac: 0.46, word: "Printer error",
              wordColor: c.err, sub: "see the printer", subColor: c.sub),
    ]
    for r in rows {
        let b = r.phase.map { p in
            p == .offline || p == .idle || p == .unknown ? KnobPrintBlock(phase: p, printer: "CORE One") : block(p)
        }
        let f = face(b)
        let name = r.phase?.rawValue ?? "none"
        #expect(f.look == r.look, "\(name)")
        #expect(f.ringColor == r.ring && f.head == r.head && f.pulses == r.pulses, "\(name) ring")
        #expect(abs(f.ring - r.frac) < 1e-9, "\(name) frac")
        #expect(f.word == r.word && f.sub == r.sub, "\(name): \(f.word) / \(f.sub)")
        if let w = r.wordColor { #expect(f.wordColor == w, "\(name) word colour") }
        if let s = r.subColor { #expect(f.subColor == s, "\(name) sub colour") }
        #expect(f.small.isEmpty, "\(name) file line")
    }
    let finishing = face(block(.finishing))
    #expect(finishing.value == "100" && finishing.unit == "%" && finishing.valueColor == c.text && finishing.unitColor == c.sub)
    #expect(face(block(.attention, progress: 0)).sub == "see the printer")
    #expect(face(block(.paused)).heroProgress == 0.46)
}

@Test func printingShowsTimeLeftRoundedUpAndTheReadyTime() {
    let b = block(.printing, progress: 42, endsAt: t0 + 102 * 60)
    let f = face(b)
    #expect(f.look == .progress && f.ringColor == c.accent && f.head && f.sweeps)
    #expect(f.value == "1:42" && f.unit == "h left" && f.valueColor == c.text && f.unitColor == c.sub)
    #expect(f.small == "Desk lamp \u{2022} PLA" && f.smallColor == c.small)
    let ready = KnobPrintFace.hhmm(t0 + 102 * 60, offset: 0)
    #expect(f.sub == "done \(ready)" && f.subColor == c.accent)
    #expect(face(b, at: now.addingTimeInterval(1)).value == "1:42")
    #expect(face(b, at: now.addingTimeInterval(60)).value == "1:41")
    #expect(face(b, at: now.addingTimeInterval(59)).value == "1:42")
    let short = face(block(.printing, endsAt: t0 + 41 * 60 + 1))
    #expect(short.value == "42" && short.unit == "min left")
    #expect(face(block(.printing, endsAt: t0 - 5)).value == "0")
    #expect(face(b, zone: nil).sub == "done in 1 h 42 m")
    #expect(face(block(.preparing, endsAt: t0 + 600)).sweeps == false)
}

@Test func theReadyTimeIsInTheUsersZone() {
    let at = Int64(1_800_003_000)
    #expect(KnobPrintFace.hhmm(at, offset: 0) == String(format: "%02d:%02d", at % 86400 / 3600, at / 60 % 60))
    #expect(KnobPrintFace.hhmm(17 * 3600 + 30 * 60, offset: 0) == "17:30")
    #expect(KnobPrintFace.hhmm(17 * 3600 + 30 * 60, offset: 3 * 3600) == "20:30")
    #expect(KnobPrintFace.hhmm(30 * 60, offset: -3600) == "23:30")
    let zone = TimeZone(identifier: "Europe/Moscow")!
    let f = face(block(.printing, endsAt: 17 * 3600 + 30 * 60), at: Date(timeIntervalSince1970: 3600), zone: zone)
    #expect(f.sub == "done 20:30")
}

@Test func theColourChangeLineShowsWithinTenMinutes() {
    let soon = face(block(.printing, endsAt: t0 + 3600, changeAt: t0 + 5 * 60 - 30))
    #expect(soon.sub == "colour change: 5 min" && soon.subColor == c.wait)
    #expect(face(block(.printing, endsAt: t0 + 3600, changeAt: t0 + 600)).sub == "colour change: 10 min")
    #expect(face(block(.printing, endsAt: t0 + 3600, changeAt: t0 + 601)).sub.hasPrefix("done "))
    #expect(face(block(.printing, endsAt: t0 + 3600, changeAt: t0 - 1)).sub.hasPrefix("done "))
}

@Test func withoutAnEstimateTheFaceNamesThePhase() {
    let printing = face(block(.printing, progress: 12))
    #expect(printing.value.isEmpty && printing.word == "Printing" && printing.wordColor == c.accent)
    #expect(printing.sub == "12% done" && printing.subColor == c.sub)
    let preparing = face(block(.preparing, progress: 0))
    #expect(preparing.word == "Preparing" && preparing.sub.isEmpty)
}

@Test func heatingShowsTheNozzleAndTheHeatUpRing() {
    let t = KnobPrintBlock.Temps(nozzle: 182, nozzleTarget: 240, bed: 72, bedTarget: 85)
    let f = face(block(.heating, progress: 0, temps: t))
    #expect(f.value == "182\u{B0}" && f.unit == "/ 240\u{B0}" && f.valueColor == KnobPrintFace.mix(c.text, c.heat, 0.5))
    #expect(f.sub == "heating \u{2022} bed 72\u{B0}" && f.subColor == c.heat)
    #expect(f.ringColor == c.heat && f.head && !f.sweeps)
    let heat = (182.0 - 25) / (240 - 25) * 0.6 + (72.0 - 22) / (85 - 22) * 0.4
    #expect(abs(f.ring - heat) < 1e-9 && abs(f.heroHeat - heat) < 1e-9 && f.heroProgress == 0)
    #expect(f.small == "Desk lamp \u{2022} PLA")
    let cold = face(block(.heating, progress: 0))
    #expect(cold.word == "Heating" && cold.wordColor == c.heat && cold.ring == 0)
}

@Test func theReadyFaceNamesAWarmBedOrNozzle() {
    func ready(_ n: Int, _ b: Int) -> KnobPrintFace {
        face(KnobPrintBlock(phase: .idle, temps: .init(nozzle: n, nozzleTarget: 0, bed: b, bedTarget: 0), printer: "CORE One"))
    }
    #expect(ready(25, 62).sub == "bed 62\u{B0}" && ready(25, 62).subColor == c.heat)
    #expect(ready(215, 40).sub == "nozzle 215\u{B0}")
    #expect(ready(215, 41).sub == "bed 41\u{B0}")
    #expect(ready(40, 40).sub == "CORE One" && ready(40, 40).subColor == c.small)
    let unknown = face(KnobPrintBlock(phase: .unknown, temps: .init(nozzle: 215, nozzleTarget: 0, bed: 62, bedTarget: 0),
                                      printer: "CORE One"))
    #expect(unknown.word == "Idle" && unknown.sub == "CORE One")
}

@Test func doneTellsHowLongItTookAndDimsAfterTenMinutes() {
    let start = t0 - 2 * 3600 - 14 * 60 - 600
    let fresh = face(block(.finished, startedAt: start, event: .init(kind: "finished", at: t0 - 599)))
    #expect(fresh.sub == "2 h 14 m \u{2022} take it out" && !fresh.dimmed)
    #expect(face(block(.finished, startedAt: start, event: .init(kind: "finished", at: t0 - 600))).dimmed)
    #expect(!face(block(.finished, startedAt: start)).dimmed, "no finish event and never seen: bright")
    #expect(!face(block(.finished, startedAt: start), doneSince: now.addingTimeInterval(-599)).dimmed)
    #expect(face(block(.finished, startedAt: start), doneSince: now.addingTimeInterval(-600)).dimmed, "10 min after first shown")
    #expect(face(block(.finished, startedAt: start, event: .init(kind: "finished", at: t0 - 700)),
                 doneSince: now.addingTimeInterval(-60)).dimmed, "the earlier of the event and the first show")
    #expect(face(block(.finished, startedAt: start)).sub == "take it out")
    #expect(KnobPrintLive.doneJob(block(.finished, startedAt: start)) == start)
    #expect(KnobPrintLive.doneJob(block(.finished)) == 0 && KnobPrintLive.doneJob(block(.printing)) == nil)
    #expect(KnobPrintLive.doneJob(block(.finished, event: .init(kind: "finished", at: 42))) == 42)
    #expect(KnobPrintLive.doneJob(block(.finished, startedAt: 7, event: .init(kind: "finished", at: 42))) == 7)
    #expect(!face(block(.printing, endsAt: t0 + 60)).dimmed)
    #expect(KnobPrintFace.duration(14 * 60) == "14 min" && KnobPrintFace.duration(59 * 60 + 29) == "59 min")
    #expect(KnobPrintFace.duration(59 * 60 + 30) == "1 h 00 m")
}

@MainActor @Test func theDoneClockKeepsTheEarliestTimeLikeDoneDim() {
    let clock = KnobPrintDoneClock()
    let fin = Date(timeIntervalSince1970: 1000), shown = Date(timeIntervalSince1970: 1000 + 25 * 60)
    #expect(clock.since(7, finishedAt: fin, now: shown) == fin, "the finish event is earlier than the first show")
    #expect(clock.since(7, finishedAt: nil, now: shown.addingTimeInterval(300)) == fin,
            "the event aged out: the kept time stays, the face stays dim")
    #expect(clock.since(7, finishedAt: nil, now: shown.addingTimeInterval(900)) == fin, "a new view of the same job")
    let late = Date(timeIntervalSince1970: 9000)
    #expect(clock.since(8, finishedAt: nil, now: late) == late, "the next job starts fresh")
    #expect(clock.since(nil, finishedAt: nil, now: late) == nil, "leaving Done forgets it, as done_seen does")

    let keyless = KnobPrintDoneClock()
    #expect(keyless.since(1000, finishedAt: fin, now: shown) == fin)
    #expect(keyless.since(0, finishedAt: nil, now: shown.addingTimeInterval(400)) == fin,
            "a key that falls to 0 when the event ages out is the same job")
    #expect(keyless.since(0, finishedAt: nil, now: late) == fin)

    let fresh = KnobPrintDoneClock()
    #expect(fresh.since(0, finishedAt: nil, now: late) == late, "no event at all: 10 min from the first show")

    var b = block(.finished, startedAt: nil, event: .init(kind: "finished", at: 1000))
    #expect(KnobPrintLive.doneKey(b) == .init(job: 1000, finishedAt: fin))
    b.event = nil
    #expect(KnobPrintLive.doneKey(b) == .init(job: 0, finishedAt: nil))
    let aged = face(block(.finished), at: shown.addingTimeInterval(300), doneSince: fin)
    #expect(aged.dimmed, "dimmed after the event aged out")
}

@Test func offlineWithoutAnyStateShowsPrinterOff() {
    let f = face(nil, link: .offline)
    #expect(f.look == .none && f.word == "Printer off" && f.sub == "offline")
}

@Test func linkStatesFollowPrintTextLink() {
    let connecting = face(nil, link: .connecting)
    #expect(connecting.word == "Connecting" && connecting.wordColor == c.dimWord && connecting.sub.isEmpty && connecting.small.isEmpty)
    let offline = face(block(.printing, endsAt: t0 + 600), link: .offline)
    #expect(offline.value == "10" && offline.sub == "offline" && offline.subColor == c.sub && offline.small.isEmpty)
    #expect(face(block(.printing, endsAt: t0 + 600)).small == "Desk lamp \u{2022} PLA")
}

@Test func linesTooWideForTheirLabelLoseTheirEnd() {
    var b = block(.printing, endsAt: t0 + 600)
    b.name = "A very long name for a desk lamp shade"
    b.material = "PLA/PETG"
    let small = face(b).small
    #expect(small.hasSuffix("... \u{2022} PLA/PETG") && small.count < b.name.count)
    let th = KnobTheme.standard
    #expect(KnobFaceRender.textWidth(small, th.print.small.fontPx, theme: th) <= th.print.small.widthPx)
}

@Test func namesFoldToASCIIForTheFace() {
    var b = block(.printing, endsAt: t0 + 600)
    b.name = "Лампа"
    b.material = "PLA–CF"
    b.printer = "Принтер"
    #expect(face(b).small == "Lampa \u{2022} PLA-CF")
    #expect(face(KnobPrintBlock(phase: .idle, printer: "Принтер")).sub == "Printer")
}

@Test func aLongLineKeepsItsLastPart() {
    let w: (String) -> Double = { Double($0.count) }
    #expect(KnobPrintFace.fit("Desk lamp shade \u{2022} PLA/PETG", width: 23, measure: w) == "Desk lamp... \u{2022} PLA/PETG")
    #expect(KnobPrintFace.fit("Desk lamp shade \u{2022} PLA/PETG", width: 20, measure: w) == "Desk l... \u{2022} PLA/PETG")
    #expect(KnobPrintFace.fit("short", width: 20, measure: w) == "short")
    #expect(KnobPrintFace.fit("A very long file name here", width: 12, measure: w) == "A very lo...")
    #expect(KnobPrintFace.fit("Lamp \u{2022} a material too long", width: 12, measure: w) == "Lamp \u{2022} a...")
    #expect(KnobPrintFace.fit("abc", width: 2, measure: w) == "")
    #expect(KnobPrintFace.fit("a b \u{2022} c \u{2022} PLA", width: 10, measure: w) == "a... \u{2022} PLA")
}

@Test func theHeadSweepsOnlyWhilePrintingAndThePulseOnlyInAttentionAndError() {
    #expect(KnobPrintFace.headX(sweeps: true, look: .progress, t: 0.6) == sin(0.6 * 2.6) * 20)
    #expect(KnobPrintFace.headX(sweeps: true, look: .progress, t: nil) == 0)
    #expect(KnobPrintFace.headX(sweeps: false, look: .heating, t: 3) == 0)
    #expect(KnobPrintFace.headX(sweeps: false, look: .progress, t: 3) == 30)
    #expect(KnobPrintFace.pulse(.attention, t: 0) == 0.35 + 0.65 * 0.5)
    #expect(KnobPrintFace.pulse(.error, t: nil) == 1)
    #expect(KnobPrintFace.pulse(.progress, t: 1) == 1)
    #expect(face(block(.printing, endsAt: t0 + 60)).moves && face(block(.attention)).moves)
    #expect(!face(block(.preparing, endsAt: t0 + 60)).moves && !face(block(.finished)).moves && !face(nil).moves)
    #expect(KnobPrintLive.moves(face(block(.attention)), reduceMotion: false))
    #expect(!KnobPrintLive.moves(face(block(.attention)), reduceMotion: true))
    #expect(KnobPrintLive.ticks(animated: true, onScreen: true, windowVisible: true))
    #expect(!KnobPrintLive.ticks(animated: true, onScreen: true, windowVisible: false))
    #expect(!KnobPrintLive.ticks(animated: false, onScreen: true, windowVisible: true))
}

@Test func theBedSinksAsThePartGrowsAndTheHeadStaysUp() {
    let empty = face(KnobPrintBlock(phase: .idle, printer: "CORE One"))
    let half = face(block(.printing, progress: 50, endsAt: t0 + 60))
    let done = face(block(.finished))
    #expect(KnobPrintHero.bedY(empty) == KnobPrintHero.partTop)
    #expect(KnobPrintHero.bedY(half) == KnobPrintHero.partTop + 24)
    #expect(KnobPrintHero.bedY(done) == KnobPrintHero.partTop + 48)
    #expect(KnobPrintHero.bedY(face(block(.heating, progress: 50))) == KnobPrintHero.partTop)
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func heroMatchesCindersPrintDraw() throws {
    let d = "components/print/print_draw.c"
    func num(_ name: String) throws -> Double {
        let v = try cinderMatch(d, "#define \(name) \\(?(-?[\\d.]+)f?\\)?\\n")
        return try #require(Double(v), "\(name)")
    }
    func col(_ name: String) throws -> RGB {
        try #require(RGB(hex: "#" + (try cinderMatch(d, "#define COL_\(name) 0x([0-9A-Fa-f]{6})"))), "\(name)")
    }
    typealias H = KnobPrintHero
    #expect(try num("BODY_W") == H.bodyW && num("BODY_H") == H.bodyH && num("HEAD_Y") == H.headY)
    #expect(try num("PART_MAX") == H.partMax && num("LAYER") == H.layer)
    #expect(try num("HEAD_SWEEP") == H.headSweep && num("HEAD_PARK") == H.headPark)
    #expect(try cinderMatch(d, #"#define PART_TOP \((HEAD_Y \+ 9\.0f)\)"#) == "HEAD_Y + 9.0f" && H.partTop == H.headY + 9)
    #expect(try col("BODY") == H.body && col("ROD") == H.rod && col("BED") == H.bed && col("BED_HEAT") == H.bedHeat)
    #expect(try col("GANTRY") == H.gantry && col("HEAD") == H.head && col("WIN_TOP") == H.winTop)
    #expect(try col("WIN_BOT") == H.winBottom && col("WIN_OFF") == H.winOff && col("LIGHT") == H.light)
    #expect(try cinderMatch(d, #"case PRINT_LOOK_STOPPED: return (0x[0-9A-F]{6});"#) == "0x333333")
    #expect(try cinderMatch(d, #"if \(phase == PRINT_PRINTING\) return \(float\)\(sin\(t \* ([\d.]+)\) \* HEAD_SWEEP\);"#) == "2.6")
    #expect(try cinderMatch(d, #"return \(float\)\((0\.35 \+ 0\.65 \* \(0\.5 \+ 0\.5 \* sin\(t \* 5\)\))\);"#)
            == "0.35 + 0.65 * (0.5 + 0.5 * sin(t * 5))")
    #expect(try cinderMatch(d, #"c->win_top = off \? COL_WIN_OFF : print_mix\(COL_WIN_TOP, light_of\(l\), ([\d.]+)f \* p->pulse\);"#) == "0.18")
    #expect(try cinderMatch("components/print/print.c", #"case PRINT_LOOK_PAUSED: out->col = print_scale\(PRINT_COL_ACCENT, ([\d.]+)f\)"#) == "0.5")
    #expect(try cinderMatch("components/print/print.c", #"(n \* 0\.6f \+ d \* 0\.4f)"#) == "n * 0.6f + d * 0.4f")
    #expect(try cinderMatch("main/print_view.c", #"static const np_ring_t EDGE = \{[^}]*\.r = (NP_EDGE_R), \.hw = NP_EDGE_HW\}"#) == "NP_EDGE_R")
    #expect(try cinderMatch("main/print_view.c", #"st\.track = (PRINT_COL_TRACK);"#) == "PRINT_COL_TRACK")
}

@Test(.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func printWordsMatchCindersPrintC() throws {
    let src = try String(contentsOf: try #require(cinderDir).appendingPathComponent("firmware/components/print/print.c"), encoding: .utf8)
    let literals = [
        #"#define SEP " \xE2\x80\xA2 ""#, #"word(t, "Printer off", PRINT_COL_DIMWORD)"#, #"word(t, "Idle", PRINT_COL_SUB)"#,
        #"word(t, "Ready", PRINT_COL_TEXT)"#, #"word(t, "Heating", PRINT_COL_HEAT)"#,
        #"b->phase == PRINT_PREPARING ? "Preparing" : "Printing", PRINT_COL_ACCENT"#, #"sub(t, PRINT_COL_SUB, "%d%% done", b->progress)"#,
        #"sub(t, PRINT_COL_WAIT, "colour change: %d min", ch)"#, #"sub(t, PRINT_COL_ACCENT, "done %s", ready)"#,
        #"sub(t, PRINT_COL_ACCENT, "done in %s", d)"#, #"sub(t, PRINT_COL_ACCENT, "finishing up")"#,
        #"word(t, "Paused", PRINT_COL_WAIT)"#, #"sub(t, PRINT_COL_SUB, "at %d%%", p)"#, #"word(t, "Check printer", PRINT_COL_WAIT)"#,
        #"k1 ? "paused" SEP "%d%% done""#, #"sub(t, PRINT_COL_SUB, "see the printer")"#, #"word(t, "Done", PRINT_COL_DONE)"#,
        #"sub(t, PRINT_COL_SUB, "%s" SEP "take it out", d)"#, #"sub(t, PRINT_COL_SUB, "take it out")"#,
        #"word(t, "Stopped", PRINT_COL_SUB)"#, #"word(t, "Printer error", PRINT_COL_ERR)"#,
        #"sub(t, PRINT_COL_HEAT, "heating" SEP "bed %d\xC2\xB0", b->bed)"#, #"snprintf(t->unit, sizeof t->unit, "/ %d\xC2\xB0", b->nozzle_target)"#,
        #"snprintf(t->val, sizeof t->val, "%d\xC2\xB0", b->nozzle)"#, #"sub(t, PRINT_COL_HEAT, "bed %d\xC2\xB0", b->bed)"#,
        #"sub(t, PRINT_COL_HEAT, "nozzle %d\xC2\xB0", b->nozzle)"#, #"sub(t, PRINT_COL_SMALL, "%s", b->printer)"#,
        #"sub(t, PRINT_COL_DIMWORD, "%s", b->printer)"#, #"snprintf(val, vcap, "%d:%02d", min / 60, min % 60);"#,
        #"*unit = "h left";"#, #"*unit = "min left";"#, #"snprintf(buf, cap, "%lld h %02lld m", m / 60, m % 60);"#,
        #"snprintf(buf, cap, "%lld min", m);"#, #"snprintf(out, cap, "%s" SEP "%s", b->name, b->material);"#,
        #"snprintf(t->val, sizeof t->val, "100");"#, #"word(t, "Connecting", PRINT_COL_DIMWORD);"#, #"sub(t, PRINT_COL_SUB, "offline");"#,
        #"return s < 0 ? -1 : (int)((s + 59) / 60);"#, #"long long m = s < 0 ? 0 : (s + 30) / 60;"#,
        #"if (b->bed > PRINT_WARM_C)"#, #"else if (b->nozzle > PRINT_WARM_C)"#, #"if (in < 0 || in > CHANGE_SOON_S) return false;"#,
        #"*min = (int)ceil(in / 60);"#,
    ]
    for l in literals { #expect(src.contains(l), "print.c no longer has \(l)") }
    let heat = try cinderMatch("components/print/print.c",
                               #"b->nozzle_target > (\d+) \? clamp01\(\(b->nozzle - (\d+)\.0f\) / \(b->nozzle_target - (\d+)\.0f\)\)"#)
    #expect(Double(heat) == KnobPrintFace.nozzleHeatFrom)
    let bed = try cinderMatch("components/print/print.c",
                              #"b->bed_target > (\d+) \? clamp01\(\(b->bed - (\d+)\.0f\) / \(b->bed_target - (\d+)\.0f\)\)"#)
    #expect(Double(bed) == KnobPrintFace.bedHeatFrom)
    #expect(src.contains("b->nozzle_target > 25 ? clamp01((b->nozzle - 25.0f) / (b->nozzle_target - 25.0f))"))
    #expect(src.contains("b->bed_target > 22 ? clamp01((b->bed - 22.0f) / (b->bed_target - 22.0f))"))
    let mixK = try cinderMatch("components/print/print.c", #"t->val_col = print_mix\(PRINT_COL_TEXT, PRINT_COL_HEAT, ([\d.]+)f\);"#)
    #expect(Double(mixK) == KnobPrintFace.heatValueMix)
}

@MainActor
private func render(_ f: KnobPrintFace, t: Double? = nil) throws -> CGImage {
    let r = ImageRenderer(content: KnobFaceView(.print(f), t: t).frame(width: 466, height: 466))
    r.scale = 1
    return try #require(r.cgImage)
}

private func pixel(_ img: CGImage, _ x: Int, _ y: Int) -> RGB {
    var buf = [UInt8](repeating: 0, count: 4)
    let ctx = CGContext(data: &buf, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(img, in: CGRect(x: -x, y: y - img.height + 1, width: img.width, height: img.height))
    return RGB(r: buf[0], g: buf[1], b: buf[2])
}

private func near(_ a: RGB, _ b: RGB, _ tol: Int = 12) -> Bool {
    abs(Int(a.r) - Int(b.r)) <= tol && abs(Int(a.g) - Int(b.g)) <= tol && abs(Int(a.b) - Int(b.b)) <= tol
}

private func save(_ img: CGImage, _ name: String) {
    guard let dir = ProcessInfo.processInfo.environment["KNOB_SNAPSHOT_DIR"] else { return }
    let url = URL(fileURLWithPath: dir).appendingPathComponent("print-" + name + ".png")
    guard let dest = CGImageDestinationCreateWithURL(url as CFURL, "public.png" as CFString, 1, nil) else { return }
    CGImageDestinationAddImage(dest, img, nil)
    CGImageDestinationFinalize(dest)
}

@MainActor @Test func printFacesRenderToTheFirmwareLayout() throws {
    let ring = KnobTheme.standard.print.ring
    let top = Int(233 - ring.radiusPx), right = Int(233 + ring.radiusPx)

    let ready = try render(face(KnobPrintBlock(phase: .idle, printer: "CORE One")))
    save(ready, "ready")
    #expect(near(pixel(ready, 233, top), c.track, 4), "track only at 12 o'clock")
    #expect(near(pixel(ready, right, 233), c.track, 4))
    #expect(near(pixel(ready, 233, 122 - 50), c.accent, 30), "the printer's orange top")
    #expect(near(pixel(ready, 233, 122 - 32), KnobPrintFace.mix(KnobPrintHero.winTop, KnobPrintHero.light, 0.18), 10), "chamber light on")
    let readyText = (262..<296).flatMap { y in (190..<280).map { x in pixel(ready, x, y) } }
    #expect(readyText.contains { near($0, c.text, 30) }, "Ready in white")

    let printing = try render(face(block(.printing, progress: 25, endsAt: t0 + 102 * 60)))
    save(printing, "printing")
    #expect(near(pixel(printing, right, 233), ring.headColor, 30), "the bright head at 25 %, 3 o'clock")
    #expect(near(pixel(printing, 233 - Int(ring.radiusPx), 233), c.track, 4), "9 o'clock still track")
    #expect(near(pixel(printing, 233 + 157, 233 - 157), c.accent, 20), "1:30 is lit")
    let bedY = 122 + KnobPrintHero.bedY(face(block(.printing, progress: 25, endsAt: t0))) * 0.95
    #expect(near(pixel(printing, 233, Int(bedY - 4)), c.fil, 50), "the part sits on the bed")
    let sub = (310..<344).flatMap { y in (160..<310).map { x in pixel(printing, x, y) } }
    #expect(sub.contains { near($0, c.accent, 40) }, "done HH:MM in the accent")

    let attention = face(block(.attention))
    let dimSign = try render(attention, t: -Double.pi / 10)
    let fullSign = try render(attention, t: Double.pi / 10)
    save(fullSign, "attention")
    #expect(near(pixel(fullSign, 233, 122), c.wait, 30), "the ! at full light")
    #expect(!near(pixel(dimSign, 233, 122), c.wait, 30), "the ! at the low end of the pulse")
    #expect(near(pixel(fullSign, 233, top), c.wait, 30))

    let off = try render(face(nil))
    save(off, "off")
    #expect(near(pixel(off, 233, 122 - 30), KnobPrintHero.winOff, 4), "chamber dark when off")

    let heat = try render(face(block(.heating, progress: 0, temps: .init(nozzle: 182, nozzleTarget: 240, bed: 72, bedTarget: 85))))
    save(heat, "heating")
    let done = try render(face(block(.finished, startedAt: t0 - 8000, event: .init(kind: "finished", at: t0 - 60))))
    save(done, "done")
    #expect(near(pixel(done, right, 233), c.done, 20))
    let dimmed = try render(face(block(.finished), doneSince: now.addingTimeInterval(-601)))
    save(dimmed, "done-dimmed")
    #expect(Int(pixel(dimmed, right, 233).b) < Int(pixel(done, right, 233).b) - 60, "the Done face dims itself")

    for (k, lit) in [(0, c.dotOn), (1, c.dotOff)] {
        let at = KnobFaceRender.printDotPoint(k, KnobTheme.standard.print.dots)
        #expect(near(pixel(ready, Int(at.x), Int(at.y)), lit, 20), "dot \(k)")
    }
    #expect(KnobFaceRender.printDotPoint(0, KnobTheme.standard.print.dots).x < 233, "K1 is the left dot")
}
