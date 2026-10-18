public struct AppleEventFailureLog: Sendable {
    public static let noSuchObject = -1728

    private var lastLogged: Int?

    public init() {}

    public mutating func shouldLog(_ code: Int) -> Bool {
        guard code != Self.noSuchObject, code != lastLogged else { return false }
        lastLogged = code
        return true
    }

    public mutating func succeeded() { lastLogged = nil }
}
