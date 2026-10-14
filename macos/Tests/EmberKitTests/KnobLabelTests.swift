import Foundation
import Testing
@testable import EmberKit

@Test func projectLabelMatchesCinderTable() {
    let cases: [(String, Int, String)] = [
        ("ember", 0, "ember"),
        ("learn-langu", 0, "learn-lan..."),
        ("learn-lang", 0, "learn-lang"),
        ("learn-language", 1, "learn-la... +1"),
        ("learn-lan", 1, "learn-lan +1"),
        ("learn-language", 12, "learn-l... +12"),
        ("learn-language", 250, "learn-l... +99"),
        ("ember", -1, "ember"),
        ("abcdefgh xyz", 0, "abcdefgh..."),
        ("Add search bar", 0, "Add searc..."),
        ("my-project x", 0, "my-projec..."),
        ("abcdefghi  jk", 1, "abcdefgh... +1"),
        ("a          b", 0, "a..."),
        ("abcdefghij   ", 0, "abcdefghij"),
        ("  ember  ", 0, "ember"),
        ("\u{43F}\u{440}\u{43E}-x\u{2026}", 0, "-x"),
        ("abcdefghi\u{43F}\u{43F}jk", 0, "abcdefghi..."),
        ("\u{1F642}", 3, ""),
        ("? ??? -", 1, ""),
        ("?7", 0, "?7"),
        ("", 1, ""),
    ]
    for (project, others, want) in cases {
        #expect(KnobMood.projectLabel(project, others: others) == want, "\(project) \(others)")
    }
    #expect(KnobMood.projectLabel(bytes: [0x61, 0x62, 0xD0], others: 0) == "ab", "a cut sequence leaves nothing")
    #expect(KnobMood.projectLabel("learn-language", others: 99).count == KnobMood.labelSlots + 2)
}

private func session(_ src: String, _ state: String, project: String? = nil, since: Double = 0,
                     color: String? = nil, tool: String = "claude", id: String? = nil) -> Session {
    let f = ISO8601DateFormatter()
    let p = project.map { #""project":"\#($0)","# } ?? ""
    let c = color.map { #""source_color":"\#($0)","# } ?? ""
    return try! JSONDecoder.iso.decode(Session.self, from: Data("""
    {"source":"\(src)","tool":"\(tool)","session":"\(id ?? src + state + String(since))","state":"\(state)","message":"",\(p)\(c)
     "tokens_today":0,"activity":"","context_number":false,"rate_bottom_bar":false,"rate_reset_at":0,"rate_reset":false,
     "updated_at":"\(f.string(from: Date(timeIntervalSince1970: 100)))",
     "state_since":"\(f.string(from: Date(timeIntervalSince1970: since)))"}
    """.utf8))
}

@Test func waitingLabelMatchesCinderLeadInfo() {
    let one = KnobMood(sessions: [session("m4", "waiting", project: "ember")])
    #expect(one.host == "ember" && one.sub == "" && one.lead == "M4", "one session, one host: no +N, no host line")
    let three = KnobMood(sessions: [session("m4", "waiting", project: "ember", since: 3),
                                    session("m4", "waiting", since: 2), session("m4", "waiting", since: 1)])
    #expect(three.host == "ember +2" && three.sub == "", "+N counts the other sessions, not hosts")
    let hosts = KnobMood(sessions: [session("macbook-pro", "waiting", project: "learn-language", since: 3),
                                    session("m4", "waiting", since: 2), session("m4", "waiting", since: 1)])
    #expect(hosts.host == "learn-la... +2" && hosts.sub == "MACBOOK-PR", "two hosts: host line")
    let bare = KnobMood(sessions: [session("m4", "waiting", since: 4), session("m5", "waiting", since: 3),
                                   session("m6", "waiting", since: 2), session("m4", "waiting", since: 1)])
    #expect(bare.host == "M4 +2" && bare.sub == "", "no project: the host label")
    let folded = KnobMood(sessions: [session("m4", "waiting", project: "??", since: 2), session("m5", "waiting", since: 1)])
    #expect(folded.host == "M4 +1" && folded.sub == "", "no letter or digit: the host label")
}

@Test func waitingLeadIsTheNewestWaiter() {
    let m = KnobMood(sessions: [session("m4", "waiting", project: "ember", since: 1, tool: "claude"),
                                session("m4", "waiting", project: "cinder", since: 2, tool: "claude"),
                                session("m5", "waiting", project: "learn-language", since: 3, color: "#b48cff", tool: "codex"),
                                session("m6", "running", since: 9)])
    #expect(m.mood == .waiting && m.host == "learn-la... +2" && m.sub == "M5" && m.lead == "M5 +1")
    #expect(m.tool == "codex" && m.hostColor == RGB(r: 0xB4, g: 0x8C, b: 0xFF))
}

@Test func doneLabelNamesTheNewestDoneSession() {
    let m = KnobMood(sessions: [session("m4", "done", project: "ember", since: 1),
                                session("m5", "done", project: "cinder", since: 2, tool: "codex")])
    #expect(m.mood == .done && m.host == "cinder +1" && m.sub == "M5" && m.tool == "codex")
    #expect(m.showsHost && m.shownSub == "M5")
    let bare = KnobMood(sessions: [session("m4", "done", since: 1)])
    #expect(bare.host == "M4" && bare.sub == "" && bare.showsHost)
    let waiting = KnobMood(sessions: [session("m4", "waiting", project: "ember", since: 1),
                                      session("m5", "done", project: "cinder", since: 2)])
    #expect(waiting.mood == .waiting && waiting.host == "ember" && waiting.sub == "", "waiting beats a newer done")
}

@Test func hostLineOnlyOnWaitingAndDone() {
    #expect(KnobMood(mood: .waiting, host: "ember", sub: "M4").shownSub == "M4")
    #expect(KnobMood(mood: .done, host: "ember", sub: "M4").shownSub == "M4")
    #expect(KnobMood(mood: .working, host: "ember", sub: "M4").shownSub == "")
    #expect(KnobMood(mood: .error, host: "ember", sub: "M4").shownSub == "")
}
