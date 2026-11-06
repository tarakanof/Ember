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

    public var headerTitle: LocalizedStringResource {
        guard let project = session.project else { return title }
        let state = String(localized: stateName)
        return session.source.isEmpty
            ? "\(project) — \(state)"
            : LocalizedStringResource("\(project) — \(state) · \(session.source)",
                                      comment: "Menu header: the lead session's project, its state, then the Mac it runs on (\"learn-language — Waiting · m4\").")
    }

    public func displayName(multiHost: Bool) -> String? {
        guard let project = session.project else { return nil }
        return multiHost && !session.source.isEmpty ? "\(session.source) · \(project)" : project
    }

    public func rowTitle(multiHost: Bool) -> LocalizedStringResource {
        guard let name = displayName(multiHost: multiHost) else { return title }
        return "\(name) — \(String(localized: stateName))"
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
