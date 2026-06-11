import Foundation

/// The Agent pane's nine producer.env toggles. Read uses the producer's default
/// semantics (envTrue = default-on, envOn = default-off); apply writes "true"/"false".
/// Ports the retired Go menu's form.go display half.
public struct DisplaySettings: Equatable, Sendable {
    public var contextPct: Bool
    public var ratePct: Bool
    public var activityDetail: Bool
    public var activityTrail: Bool
    public var contextNumber: Bool
    public var rateBottomBar: Bool
    public var rateReset: Bool
    public var sourceCard: Bool
    public var sessionBar: Bool

    public init(reading env: EnvFile) {
        contextPct = envTrue(env.get(SettingsKeys.contextPct))
        ratePct = envTrue(env.get(SettingsKeys.ratePct))
        activityDetail = envTrue(env.get(SettingsKeys.activityDetail))
        activityTrail = envTrue(env.get(SettingsKeys.activityTrail))
        contextNumber = envOn(env.get(SettingsKeys.contextNumber))
        rateBottomBar = envOn(env.get(SettingsKeys.rateBottomBar))
        rateReset = envOn(env.get(SettingsKeys.rateReset))
        sourceCard = envTrue(env.get(SettingsKeys.sourceCard))
        sessionBar = envTrue(env.get(SettingsKeys.sessionBar))
    }

    public func apply(to env: inout EnvFile) {
        func b(_ v: Bool) -> String { v ? "true" : "false" }
        env.set(SettingsKeys.contextPct, b(contextPct))
        env.set(SettingsKeys.ratePct, b(ratePct))
        env.set(SettingsKeys.activityDetail, b(activityDetail))
        env.set(SettingsKeys.activityTrail, b(activityTrail))
        env.set(SettingsKeys.contextNumber, b(contextNumber))
        env.set(SettingsKeys.rateBottomBar, b(rateBottomBar))
        env.set(SettingsKeys.rateReset, b(rateReset))
        env.set(SettingsKeys.sourceCard, b(sourceCard))
        env.set(SettingsKeys.sessionBar, b(sessionBar))
    }

    /// The eight toggles that affect a single-session render (+ the connection's
    /// source colour), for GET /v1/preview. activity_trail is intentionally absent.
    public func draftDisplay(sourceColor: String) -> DraftDisplay {
        var d = DraftDisplay()
        d.contextPct = contextPct
        d.ratePct = ratePct
        d.activityDetail = activityDetail
        d.contextNumber = contextNumber
        d.rateBottomBar = rateBottomBar
        d.rateReset = rateReset
        d.sourceCard = sourceCard
        d.sessionBar = sessionBar
        d.sourceColor = sourceColor
        return d
    }
}

/// Three-way row-7 mode over the two env toggles. Rate wins in the renderer,
/// so the setter keeps the pair unambiguous.
public enum BottomBarMode: String, CaseIterable, Sendable, Identifiable {
    case session = "Session pixels"
    case rate = "Rate bar"
    case off = "Off"
    public var id: String { rawValue }
}

extension DisplaySettings {
    public var bottomBarMode: BottomBarMode {
        get { rateBottomBar ? .rate : (sessionBar ? .session : .off) }
        set {
            switch newValue {
            case .session: sessionBar = true; rateBottomBar = false
            case .rate:    sessionBar = false; rateBottomBar = true
            case .off:     sessionBar = false; rateBottomBar = false
            }
        }
    }
}
