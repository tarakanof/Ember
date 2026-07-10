import Foundation

/// The two producer agents managed by the unified installer: the Claude
/// heartbeat producer and the Codex producer. Each case carries the
/// metadata needed to locate its binary, its LaunchAgent plist, and the
/// per-agent config directory used for auto-detection.
public enum ProducerAgent: String, CaseIterable, Sendable {
    case claude, codex

    /// The binary name shipped in `Contents/MacOS/` of the app bundle.
    public var binaryName: String {
        self == .claude ? "ember-claude-producer" : "ember-codex-producer"
    }

    /// The LaunchAgent plist name shipped in `Contents/Library/LaunchAgents/`.
    public var plistName: String {
        self == .claude ? "com.ember.heartbeat.plist" : "com.ember.codex.plist"
    }

    /// The relative path (under `$HOME`) used to detect whether the
    /// corresponding CLI tool is installed.
    public var detectRelPath: String {
        self == .claude ? ".claude" : ".codex"
    }
}
