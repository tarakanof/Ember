import Foundation

/// The producer agents managed by the unified installer: the Claude
/// heartbeat producer, the Codex producer and the T3 Code producer.
public enum ProducerAgent: String, CaseIterable, Sendable {
    case claude, codex, t3

    /// The binary name shipped in `Contents/MacOS/` of the app bundle.
    public var binaryName: String {
        switch self {
        case .claude: "ember-claude-producer"
        case .codex: "ember-codex-producer"
        case .t3: "ember-t3-producer"
        }
    }

    /// The LaunchAgent plist name shipped in `Contents/Library/LaunchAgents/`.
    public var plistName: String { label + ".plist" }

    /// The launchd label in that plist (the service name `launchctl print`
    /// takes as `gui/<uid>/<label>`).
    public var label: String {
        switch self {
        case .claude: "com.ember.heartbeat"
        case .codex: "com.ember.codex"
        case .t3: "com.ember.t3"
        }
    }

    /// The relative path (under `$HOME`) used to detect whether the
    /// corresponding CLI tool is installed (T3 Code also honours
    /// producer.env's `EMBER_T3_HOME`).
    public var detectRelPath: String {
        switch self {
        case .claude: ".claude"
        case .codex: ".codex"
        case .t3: ".t3"
        }
    }

    /// Where (under `$HOME`) the helper's LaunchAgent records whether it last
    /// reached the server (Go: `producer.LinkStatusPath`).
    public var linkStatusRelPath: String {
        ".config/ember/\(binaryName.dropFirst("ember-".count)).link.json"
    }

    /// Whether the agent's row shows even when its tool isn't detected, so it
    /// can be turned on before the tool's first run (T3 Code's home can be
    /// moved with `--base-dir`).
    public var listedWhenUndetected: Bool { self == .t3 }
}
