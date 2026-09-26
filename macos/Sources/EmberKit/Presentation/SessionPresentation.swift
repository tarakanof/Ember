import Foundation

/// How a session reads in the menu, the Dashboard and the Dock menu.
public struct SessionPresentation: Sendable, Equatable {
    public let session: Session

    public init(_ session: Session) { self.session = session }

    /// "Claude", "Codex".
    public var toolDisplayName: String { AppNames.display(session.tool) }

    /// "Running".
    public var stateName: String { session.stateEnum.displayName }

    /// "Claude on m4 — Running"; without a source, "Claude — Running".
    public var title: String {
        session.source.isEmpty
            ? "\(toolDisplayName) — \(stateName)"
            : "\(toolDisplayName) on \(session.source) — \(stateName)"
    }

    /// What the agent is doing ("Bash: npm test"), else its message; nil when
    /// neither is set.
    public var subtitle: String? {
        if !session.activity.isEmpty { return session.activity }
        if !session.message.isEmpty { return session.message }
        return nil
    }

    /// "8% context"; nil when the agent doesn't report it.
    public func contextText(locale: Locale = .current) -> String? {
        session.contextPct.map { "\(Percent.text(Double($0), locale: locale)) context" }
    }
}
