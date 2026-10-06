import Foundation

public struct SessionPresentation: Sendable, Equatable {
    public let session: Session

    public init(_ session: Session) { self.session = session }

    public var toolDisplayName: LocalizedStringResource { AppNames.display(session.tool) }

    public var stateName: LocalizedStringResource { session.stateEnum.displayName }

    public var title: LocalizedStringResource {
        let tool = String(localized: toolDisplayName)
        let state = String(localized: stateName)
        return session.source.isEmpty
            ? "\(tool) — \(state)"
            : "\(tool) on \(session.source) — \(state)"
    }

    public var subtitle: String? { subtitle(maxLength: 80) }

    public func subtitle(maxLength: Int) -> String? {
        Self.displayText(session.activity, maxLength: maxLength)
            ?? Self.displayText(session.message, maxLength: maxLength)
    }

    public func contextText(locale: Locale = .current) -> LocalizedStringResource? {
        session.contextPct.map { "\(Percent.text(Double($0), locale: locale)) context" }
    }

    public static func displayText(_ raw: String, maxLength: Int) -> String? {
        var s = raw
        s = s.replacing(/(?s)<([A-Za-z][\w:.-]*)\b[^>]*>.*?<\/\1\s*>/, with: " ")
        s = s.replacing(/<\/?[A-Za-z][^<>]*>/, with: " ")
        s = s.replacing(/<\/?[A-Za-z][^>]*$/, with: " ")
        s = s.replacing(/<\/?$/, with: " ")
        s = String(String.UnicodeScalarView(s.unicodeScalars.map { $0.properties.generalCategory == .control ? " " : $0 }))
        s = s.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        guard !s.isEmpty, maxLength > 0 else { return nil }
        guard s.count > maxLength else { return s }
        let cut = s.prefix(max(0, maxLength - 1)).trimmingCharacters(in: .whitespaces)
        return cut + "…"
    }
}
