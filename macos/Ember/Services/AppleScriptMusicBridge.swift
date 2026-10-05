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

    func artwork() async -> (trackID: String, data: Data)? {
        guard await isRunning(), let list = await run(Self.artworkScript), list.numberOfItems == 2,
              let id = list.atIndex(1)?.stringValue, let art = list.atIndex(2) else { return nil }
        let data = art.data
        return data.isEmpty ? nil : (id, data)
    }

    func snapshot() async -> MusicPlayerInfo? {
        guard await isRunning(), let list = await run(Self.snapshotScript), list.numberOfItems >= 1 else { return nil }
        func text(_ i: Int) -> String { list.atIndex(i)?.stringValue ?? "" }
        switch text(1) {
        case "playing", "paused":
            guard list.numberOfItems >= 7 else { return nil }
            let volume = list.numberOfItems >= 8 ? list.atIndex(8).map { Int($0.int32Value) } : nil
            return MusicPlayerInfo(
                state: text(1) == "playing" ? .playing : .paused,
                name: text(2), artist: text(3), album: text(4),
                durationMs: Int64(((list.atIndex(5)?.doubleValue ?? 0) * 1000).rounded()),
                persistentID: text(6), position: list.atIndex(7)?.doubleValue, volume: volume)
        default:
            return MusicPlayerInfo(state: .stopped)
        }
    }

    func volume() async -> Int? {
        guard await isRunning(), let d = await run(Self.volumeScript), d.descriptorType != typeNull else { return nil }
        return Int(d.int32Value)
    }

    /// The knob's controls (Ember #280). `run` re-checks that Music runs right
    /// before the script, so a command never launches it.
    func perform(_ command: NowPlayingCommand) async -> Bool {
        let source: String
        switch command.action {
        case .playPause?: source = Self.playPauseScript
        case .next?: source = Self.nextScript
        case .previous?: source = Self.previousScript
        case .volume?:
            guard command.delta != 0 else { return true }
            // Clamped to an integer first, so the script text is a small literal.
            source = Self.volumeChangeScript(delta: min(max(command.delta, -100), 100))
        case nil: return false
        }
        guard await isRunning() else { return false }
        return await run(source, cache: command.action != .volume) != nil
    }

    /// Ember's Automation permission for Music: nil when macOS can't say
    /// (Music isn't running). `ask` shows the prompt when not decided yet;
    /// it names a concrete event (core/getd, what the scripts send), since
    /// wildcard event codes are reported not to prompt.
    func automationStatus(ask: Bool) async -> AccessStatus? {
        await withCheckedContinuation { cont in
            DispatchQueue.global(qos: .utility).async {
                let target = NSAppleEventDescriptor(bundleIdentifier: Self.bundleID)
                let status = AEDeterminePermissionToAutomateTarget(
                    target.aeDesc, AEEventClass(kCoreEventClass), AEEventID(kAEGetData), ask)
                switch status {
                case noErr: cont.resume(returning: .granted)
                case OSStatus(errAEEventNotPermitted): cont.resume(returning: .denied)
                case OSStatus(errAEEventWouldRequireUserConsent): cont.resume(returning: .notDetermined)
                default: cont.resume(returning: nil)
                }
            }
        }
    }

    /// `cache: false` for one-off scripts (volume deltas), so `compiled` stays small.
    private func run(_ source: String, cache: Bool = true) async -> NSAppleEventDescriptor? {
        await withCheckedContinuation { cont in
            queue.async {
                guard !NSRunningApplication.runningApplications(withBundleIdentifier: Self.bundleID).isEmpty else {
                    cont.resume(returning: nil)
                    return
                }
                let script = self.compiled[source] ?? NSAppleScript(source: source)
                if cache { self.compiled[source] = script }
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

    private static let volumeScript = #"tell application id "com.apple.Music" to get sound volume"#
    private static let playPauseScript = #"tell application id "com.apple.Music" to playpause"#
    private static let nextScript = #"tell application id "com.apple.Music" to next track"#
    private static let previousScript = #"tell application id "com.apple.Music" to previous track"#
    private static func volumeChangeScript(delta: Int) -> String {
        """
        tell application id "com.apple.Music"
            set v to (sound volume) + (\(delta))
            if v < 0 then set v to 0
            if v > 100 then set v to 100
            set sound volume to v
            return v
        end tell
        """
    }
    private static let positionScript = #"tell application id "com.apple.Music" to get player position"#
    private static let artworkScript = #"""
    tell application id "com.apple.Music"
        set t to current track
        return {(persistent ID of t), (raw data of artwork 1 of t)}
    end tell
    """#
    private static let snapshotScript = #"""
    tell application id "com.apple.Music"
        if player state is stopped then return {"stopped"}
        set t to current track
        set d to 0
        try
            set d to duration of t
        end try
        return {(player state as text), (name of t), (artist of t), (album of t), d, (persistent ID of t), player position, sound volume}
    end tell
    """#
}
