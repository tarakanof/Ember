import Foundation

/// The Agent pane's six producer.env toggles. Read uses the producer's default
/// semantics (envTrue = default-on, envOn = default-off); apply writes "true"/"false".
/// Ports the retired Go menu's form.go display half (rate-%/context-number/reset retired).
public struct DisplaySettings: Equatable, Sendable {
    public var contextPct: Bool
    public var activityDetail: Bool
    public var activityTrail: Bool
    public var rateBottomBar: Bool
    public var sourceCard: Bool
    public var sessionBar: Bool

    public init(reading env: EnvFile) {
        contextPct = envTrue(env.get(SettingsKeys.contextPct))
        activityDetail = envTrue(env.get(SettingsKeys.activityDetail))
        activityTrail = envTrue(env.get(SettingsKeys.activityTrail))
        rateBottomBar = envOn(env.get(SettingsKeys.rateBottomBar))
        sourceCard = envTrue(env.get(SettingsKeys.sourceCard))
        sessionBar = envTrue(env.get(SettingsKeys.sessionBar))
    }

    public func apply(to env: inout EnvFile) {
        func b(_ v: Bool) -> String { v ? "true" : "false" }
        env.set(SettingsKeys.contextPct, b(contextPct))
        env.set(SettingsKeys.activityDetail, b(activityDetail))
        env.set(SettingsKeys.activityTrail, b(activityTrail))
        env.set(SettingsKeys.rateBottomBar, b(rateBottomBar))
        env.set(SettingsKeys.sourceCard, b(sourceCard))
        env.set(SettingsKeys.sessionBar, b(sessionBar))
    }

    /// The six toggles that affect a single-session render (+ the connection's
    /// source colour), for GET /v1/preview. activity_trail is intentionally absent.
    /// usageCard is always true: the pane preview demonstrates the card regardless
    /// of the threshold that gates the device.
    public func draftDisplay(sourceColor: String) -> DraftDisplay {
        var d = DraftDisplay()
        d.contextPct = contextPct
        d.activityDetail = activityDetail
        d.rateBottomBar = rateBottomBar
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
