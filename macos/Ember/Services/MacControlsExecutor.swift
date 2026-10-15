import AppKit
import AudioToolbox
import CoreAudio
import CoreGraphics
import EmberKit

struct MacControlsExecutor: ControlsExecutor {
    private static let scripts = ScriptProcessRunner()
    private static let systemDefinedAuxKeys: Int16 = 8
    private static let keyDown = 0xA
    private static let keyUp = 0xB

    @MainActor
    func changeVolume(by points: Int) -> Bool {
        guard let device = Self.defaultOutput(), let current = Self.volume(device) else { return false }
        let next = min(max(current + Float32(points) / 100, 0), 1)
        if points > 0, Self.muted(device) == true { _ = Self.setMuted(device, false) }
        return Self.setVolume(device, next)
    }

    @MainActor
    func toggleMute() -> Bool {
        guard let device = Self.defaultOutput(), let muted = Self.muted(device) else { return false }
        return Self.setMuted(device, !muted)
    }

    @MainActor
    func press(_ key: MediaKey) -> Bool {
        let code: Int
        switch key {
        case .playPause: code = 16
        case .next: code = 17
        case .previous: code = 18
        }
        for state in [Self.keyDown, Self.keyUp] {
            guard let event = NSEvent.otherEvent(
                with: .systemDefined, location: .zero, modifierFlags: NSEvent.ModifierFlags(rawValue: UInt(state << 8)),
                timestamp: 0, windowNumber: 0, context: nil, subtype: Self.systemDefinedAuxKeys,
                data1: (code << 16) | (state << 8), data2: -1)?.cgEvent else { return false }
            event.post(tap: .cghidEventTap)
        }
        return true
    }

    @MainActor
    func scroll(_ axis: ScrollAxis, lines: Int) -> Bool {
        let amount = Int32(clamping: -lines)
        let event = axis == .vertical
            ? CGEvent(scrollWheelEvent2Source: nil, units: .line, wheelCount: 1, wheel1: amount, wheel2: 0, wheel3: 0)
            : CGEvent(scrollWheelEvent2Source: nil, units: .line, wheelCount: 2, wheel1: 0, wheel2: amount, wheel3: 0)
        guard let event else { return false }
        event.post(tap: .cghidEventTap)
        return true
    }

    @MainActor
    func runShortcut(_ name: String) -> ScriptLaunch {
        Self.scripts.run("/usr/bin/shortcuts", ["run", name], what: "shortcut")
    }

    @MainActor
    func runAppleScript(_ source: String) -> ScriptLaunch {
        Self.scripts.run("/usr/bin/osascript", ["-"], stdin: Data(source.utf8), what: "applescript")
    }

    func canPostEvents() async -> Bool { CGPreflightPostEventAccess() }

    static func requestEventAccess() -> Bool { CGRequestPostEventAccess() }

    static func stopScripts() { scripts.shutdown() }

    private static func address(_ selector: AudioObjectPropertySelector,
                                scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeOutput) -> AudioObjectPropertyAddress {
        AudioObjectPropertyAddress(mSelector: selector, mScope: scope, mElement: kAudioObjectPropertyElementMain)
    }

    private static func defaultOutput() -> AudioObjectID? {
        var addr = address(kAudioHardwarePropertyDefaultOutputDevice, scope: kAudioObjectPropertyScopeGlobal)
        var id = AudioObjectID(kAudioObjectUnknown)
        var size = UInt32(MemoryLayout<AudioObjectID>.size)
        let status = AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &size, &id)
        return status == noErr && id != kAudioObjectUnknown ? id : nil
    }

    private static func volume(_ device: AudioObjectID) -> Float32? {
        var addr = address(kAudioHardwareServiceDeviceProperty_VirtualMainVolume)
        var value: Float32 = 0
        var size = UInt32(MemoryLayout<Float32>.size)
        return AudioObjectGetPropertyData(device, &addr, 0, nil, &size, &value) == noErr ? value : nil
    }

    private static func setVolume(_ device: AudioObjectID, _ value: Float32) -> Bool {
        var addr = address(kAudioHardwareServiceDeviceProperty_VirtualMainVolume)
        var v = value
        return AudioObjectSetPropertyData(device, &addr, 0, nil, UInt32(MemoryLayout<Float32>.size), &v) == noErr
    }

    private static func muted(_ device: AudioObjectID) -> Bool? {
        var addr = address(kAudioDevicePropertyMute)
        var value: UInt32 = 0
        var size = UInt32(MemoryLayout<UInt32>.size)
        return AudioObjectGetPropertyData(device, &addr, 0, nil, &size, &value) == noErr ? value != 0 : nil
    }

    private static func setMuted(_ device: AudioObjectID, _ on: Bool) -> Bool {
        var addr = address(kAudioDevicePropertyMute)
        var v: UInt32 = on ? 1 : 0
        return AudioObjectSetPropertyData(device, &addr, 0, nil, UInt32(MemoryLayout<UInt32>.size), &v) == noErr
    }
}
