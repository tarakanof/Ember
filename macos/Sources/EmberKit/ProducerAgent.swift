import Foundation

public enum ProducerAgent: String, CaseIterable, Sendable {
    case claude, codex, t3

    public var binaryName: String {
        switch self {
        case .claude: "ember-claude-producer"
        case .codex: "ember-codex-producer"
        case .t3: "ember-t3-producer"
        }
    }

    public var plistName: String { label + ".plist" }

    public var label: String {
        switch self {
        case .claude: "com.ember.heartbeat"
        case .codex: "com.ember.codex"
        case .t3: "com.ember.t3"
        }
    }

    public var detectRelPath: String {
        switch self {
        case .claude: ".claude"
        case .codex: ".codex"
        case .t3: ".t3"
        }
    }

    public var linkStatusRelPath: String {
        ".config/ember/\(binaryName.dropFirst("ember-".count)).link.json"
    }

    public var listedWhenUndetected: Bool { self == .t3 }
}
