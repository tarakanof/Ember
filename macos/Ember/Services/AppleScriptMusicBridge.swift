import AppKit
import OSLog
import EmberKit

/// Guarded `tell` and one serial queue, or Music launches / NSAppleScript races (ARCHITECTURE "Apple Music pusher").
final class AppleScriptMusicBridge: MusicBridge, @unchecked Sendable {
    static let bundleID = "com.apple.Music"
    private static let log = Logger(subsystem: "com.ember.Ember", category: "music")

    private let queue = DispatchQueue(label: "com.ember.Ember.music-applescript", qos: .utility)
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
        await onQueue { self.readVolume() }
    }

    func canControl() async -> Bool {
        await automationStatus(ask: false) == .granted
    }

    /// Addressed by PID so a quit Music isn't relaunched (ARCHITECTURE "Now playing" › Music).
    func perform(_ command: NowPlayingCommand) async -> Bool {
        guard let action = command.action else { return false }
        return await onQueue {
            switch action {
            case .playPause: return self.send(Self.hook("PlPs")) != nil
            case .play: return self.send(Self.hook("Play")) != nil
            case .pause: return self.send(Self.hook("Paus")) != nil
            case .next: return self.send(Self.hook("Next")) != nil
            case .previous: return self.send(Self.hook("Prev")) != nil
            case .volume:
                guard command.delta != 0 else { return true }
                guard let cur = self.readVolume() else { return false }
                let v = min(max(cur + min(max(command.delta, -100), 100), 0), 100)
                return v == cur || self.writeVolume(v)
            }
        }
    }

    private func onQueue<T: Sendable>(_ work: @escaping @Sendable () -> T) async -> T {
        await withCheckedContinuation { cont in queue.async { cont.resume(returning: work()) } }
    }

    private static func fourCC(_ s: String) -> FourCharCode {
        s.utf8.reduce(0) { ($0 << 8) | FourCharCode($1) }
    }

    private struct Event { let cls: FourCharCode; let id: FourCharCode; var params: [(AEKeyword, NSAppleEventDescriptor)] = [] }

    private static func hook(_ id: String) -> Event { Event(cls: fourCC("hook"), id: fourCC(id)) }

    private static var volumeProperty: NSAppleEventDescriptor {
        let spec = NSAppleEventDescriptor.record().coerce(toDescriptorType: DescType(typeObjectSpecifier))!
        spec.setDescriptor(NSAppleEventDescriptor.null(), forKeyword: AEKeyword(keyAEContainer))
        spec.setDescriptor(NSAppleEventDescriptor(enumCode: DescType(formPropertyID)), forKeyword: AEKeyword(keyAEKeyForm))
        spec.setDescriptor(NSAppleEventDescriptor(typeCode: fourCC("pVol")), forKeyword: AEKeyword(keyAEKeyData))
        spec.setDescriptor(NSAppleEventDescriptor(typeCode: DescType(cProperty)), forKeyword: AEKeyword(keyAEDesiredClass))
        return spec
    }

    private func readVolume() -> Int? {
        var e = Event(cls: AEEventClass(kAECoreSuite), id: AEEventID(kAEGetData))
        e.params = [(AEKeyword(keyDirectObject), Self.volumeProperty)]
        guard let reply = send(e), let v = reply.paramDescriptor(forKeyword: AEKeyword(keyDirectObject)) else { return nil }
        return Int(v.int32Value)
    }

    private func writeVolume(_ v: Int) -> Bool {
        var e = Event(cls: AEEventClass(kAECoreSuite), id: AEEventID(kAESetData))
        e.params = [(AEKeyword(keyDirectObject), Self.volumeProperty),
                    (AEKeyword(keyAEData), NSAppleEventDescriptor(int32: Int32(v)))]
        return send(e) != nil
    }

    private func send(_ e: Event) -> NSAppleEventDescriptor? {
        guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: Self.bundleID).first else { return nil }
        let target = NSAppleEventDescriptor(processIdentifier: app.processIdentifier)
        let event = NSAppleEventDescriptor.appleEvent(withEventClass: e.cls, eventID: e.id, targetDescriptor: target,
                                                      returnID: AEReturnID(kAutoGenerateReturnID),
                                                      transactionID: AETransactionID(kAnyTransactionID))
        for (k, d) in e.params { event.setParam(d, forKeyword: k) }
        do {
            return try event.sendEvent(options: [.waitForReply, .neverInteract], timeout: 3)
        } catch {
            let code = (error as NSError).code
            if code != Self.noSuchObject { Self.log.notice("Music event failed: \(code, privacy: .public)") }
            return nil
        }
    }

    /// Names core/getd: wildcard codes don't prompt (ARCHITECTURE "Apple Music pusher").
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

    private func run(_ source: String) async -> NSAppleEventDescriptor? {
        await withCheckedContinuation { cont in
            queue.async {
                guard !NSRunningApplication.runningApplications(withBundleIdentifier: Self.bundleID).isEmpty else {
                    cont.resume(returning: nil)
                    return
                }
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

    private static let noSuchObject = -1728

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
