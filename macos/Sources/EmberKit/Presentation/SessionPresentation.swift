import Foundation

/// How a session reads in the menu, the Dashboard and the Dock menu.
public struct SessionPresentation: Sendable, Equatable {
    public let session: Session

    public init(_ session: Session) { self.session = session }

    /// "Claude", "Codex".
    public var toolDisplayName: LocalizedStringResource { AppNames.display(session.tool) }

    /// "Running".
    public var stateName: LocalizedStringResource { session.stateEnum.displayName }

    /// "Claude on m4 — Running"; without a source, "Claude — Running".
    public var title: LocalizedStringResource {
        let tool = String(localized: toolDisplayName)
        let state = String(localized: stateName)
        return session.source.isEmpty
            ? "\(tool) — \(state)"
            : "\(tool) on \(session.source) — \(state)"
    }

    /// What the agent is doing ("Bash: npm test"), else its message, cleaned
    /// for display and cut to 80 characters; nil when neither has text left.
    public var subtitle: String? { subtitle(maxLength: 80) }

    /// `subtitle` cut to `maxLength` characters (the menu uses 48).
    public func subtitle(maxLength: Int) -> String? {
        Self.displayText(session.activity, maxLength: maxLength)
            ?? Self.displayText(session.message, maxLength: maxLength)
    }

    /// "8% context"; nil when the agent doesn't report it.
    public func contextText(locale: Locale = .current) -> LocalizedStringResource? {
        session.contextPct.map { "\(Percent.text(Double($0), locale: locale)) context" }
    }

    /// Producer text as it may be shown: activity strings are raw agent
    /// output and can carry markup ("<task-notification>…</task-notification>",
    /// or a tag cut off mid-way by the producer's own truncation). Drops
    /// tagged blocks, stray and unterminated tags and control characters,
    /// collapses whitespace, and cuts to `maxLength` characters (graphemes,
    /// so an emoji or accent is never split) with an ellipsis. nil when
    /// nothing readable is left.
    public static func displayText(_ raw: String, maxLength: Int) -> String? {
        var s = raw
        // A whole element is a wrapper around machine content: drop it.
        s = s.replacing(/(?s)<([A-Za-z][\w:.-]*)\b[^>]*>.*?<\/\1\s*>/, with: " ")
        // Remaining complete tags (<br>, </x>, <x/>).
        s = s.replacing(/<\/?[A-Za-z][^<>]*>/, with: " ")
        // A tag the producer's truncation cut off ("<task-notifica…c1</"):
        // everything from its "<" on is markup. "a < b" isn't a tag.
        s = s.replacing(/<\/?[A-Za-z][^>]*$/, with: " ")
        s = s.replacing(/<\/?$/, with: " ")
        // Cc only: format characters (Cf) include the joiner inside emoji.
        s = String(String.UnicodeScalarView(s.unicodeScalars.map { $0.properties.generalCategory == .control ? " " : $0 }))
        s = s.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        guard !s.isEmpty, maxLength > 0 else { return nil }
        guard s.count > maxLength else { return s }
        let cut = s.prefix(max(0, maxLength - 1)).trimmingCharacters(in: .whitespaces)
        return cut + "…"
    }
}
