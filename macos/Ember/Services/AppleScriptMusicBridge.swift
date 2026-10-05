import AppKit
import OSLog
import EmberKit

/// Reads Music.app over Apple Events. Every script is guarded by a running
/// check, because `tell application "Music"` launches it otherwise. Scripts
/// run on one serial queue (NSAppleScript isn't thread-safe).
final class AppleScriptMusicBridge: MusicBridge, @unchecked Sendable {
    static let bundleID = "com.apple.Music"
    private static let log = Logger(subsystem: "com.ember.Ember", category: "music")

    private let queue = DispatchQueue(label: "com.ember.Ember.music-applescript", qos: .utility)
    /// Compiled scripts by source; touched only on `queue`.
    private var compiled: [String: NSAppleScript] = [:]

    func isRunning() async -> Bool {
        !NSRunningApplication.runningApplications(withBundleIdentifier: Self.bundleID).isEmpty
    }

    func position() async -> Double? {
        guard await isRunning(), let d = await run(Self.positionScript), d.descriptorType != typeNull else { return nil }
        return d.doubleValue
    }

    func artwork() async -> Data? {
        guard await isRunning(), let d = await run(Self.artworkScript), d.descriptorType != typeNull else { return nil }
        let data = d.data
        return data.isEmpty ? nil : data
    }

    func snapshot() async -> MusicPlayerInfo? {
        guard await isRunning(), let list = await run(Self.snapshotScript), list.numberOfItems >= 1 else { return nil }
        func text(_ i: Int) -> String { list.atIndex(i)?.stringValue ?? "" }
        switch text(1) {
        case "playing", "paused":
            guard list.numberOfItems >= 7 else { return nil }
            return MusicPlayerInfo(
                state: text(1) == "playing" ? .playing : .paused,
                name: text(2), artist: text(3), album: text(4),
                durationMs: Int64(((list.atIndex(5)?.doubleValue ?? 0) * 1000).rounded()),
                persistentID: text(6), position: list.atIndex(7)?.doubleValue)
        default:
            return MusicPlayerInfo(state: .stopped)
        }
    }

    /// Ember's Automation permission for Music: nil when macOS can't say
    /// (Music isn't running). `ask` shows the prompt when not decided yet.
    func automationStatus(ask: Bool) async -> AccessStatus? {
        await withCheckedContinuation { cont in
            DispatchQueue.global(qos: .utility).async {
                let target = NSAppleEventDescriptor(bundleIdentifier: Self.bundleID)
                let status = AEDeterminePermissionToAutomateTarget(target.aeDesc, typeWildCard, typeWildCard, ask)
                switch status {
                case noErr: cont.resume(returning: .granted)
                case OSStatus(errAEEventNotPermitted): cont.resume(returning: .denied)
                case OSStatus(errAEEventWouldRequireUserConsent): cont.resume(returning: .notDetermined)
                default: cont.resume(returning: nil)
                }
            }
        }
    }

    private func run(_ source: String) async -> NSAppleEventDescriptor? {
        await withCheckedContinuation { cont in
            queue.async {
                let script = self.compiled[source] ?? NSAppleScript(source: source)
                self.compiled[source] = script
                var error: NSDictionary?
                let result = script?.executeAndReturnError(&error)
                if let error {
                    let code = error[NSAppleScript.errorNumber] as? Int ?? 0
                    if code != Self.noSuchObject { Self.log.notice("Music script failed: \(code, privacy: .public)") }
                    cont.resume(returning: nil)
                } else {
                    cont.resume(returning: result)
                }
            }
        }
    }

    /// errAENoSuchObject: no artwork, or nothing playing; expected, not logged.
    private static let noSuchObject = -1728

    private static let positionScript = #"tell application id "com.apple.Music" to get player position"#
    private static let artworkScript = #"tell application id "com.apple.Music" to get raw data of artwork 1 of current track"#
    private static let snapshotScript = #"""
    tell application id "com.apple.Music"
        if player state is stopped then return {"stopped"}
        set t to current track
        set d to 0
        try
            set d to duration of t
        end try
        return {(player state as text), (name of t), (artist of t), (album of t), d, (persistent ID of t), player position}
    end tell
    """#
}
