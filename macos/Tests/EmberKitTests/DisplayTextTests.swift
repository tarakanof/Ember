import Testing
import Foundation
@testable import EmberKit

@Test(arguments: [
    // Seen on device: a producer cut a tag off mid-way.
    ("<task-notifica…2e1aa3e6c1</", nil),
    ("<task-notification><task-id>b9r</task-id></task-notification> Bash: go test", "Bash: go test"),
    ("Bash: sed -n 1,237p\n\n   cmd/ember/device.go", "Bash: sed -n 1,237p cmd/ember/device.go"),
    ("Edit <b>main.go</b> now", "Edit now"),
    ("Line<br/>break", "Line break"),
    ("Bash: echo hi > out.txt && test 1 < 2", "Bash: echo hi > out.txt && test 1 < 2"),
    ("tab\there\u{7}bell", "tab here bell"),
    ("   ", nil),
] as [(String, String?)])
func displayTextCleansProducerText(raw: String, want: String?) {
    #expect(SessionPresentation.displayText(raw, maxLength: 80) == want)
}

@Test func displayTextTruncatesOnGraphemes() {
    let s = SessionPresentation.displayText("Reading 👩‍👩‍👧‍👦 family.swift and more", maxLength: 10)
    #expect(s == "Reading 👩‍👩‍👧‍👦…")
    #expect(s?.count == 10)
    #expect(SessionPresentation.displayText("short", maxLength: 10) == "short")
}

@Test func subtitleFallsBackToTheMessageWhenActivityIsOnlyMarkup() throws {
    let json = #"{"tool":"claude","state":"running","activity":"<task-notifica…2e1aa3e6c1</","message":"Bash tool"}"#
    let s = try JSONDecoder().decode(Session.self, from: Data(json.utf8))
    #expect(SessionPresentation(s).subtitle == "Bash tool")
    #expect(SessionPresentation(s).subtitle(maxLength: 5) == "Bash…")
}
