import Foundation

/// The Display tab's seven producer.env toggles. Read uses the producer's default
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

    public init(reading env: EnvFile) {
        contextPct = envTrue(env.get(SettingsKeys.contextPct))
        ratePct = envTrue(env.get(SettingsKeys.ratePct))
        activityDetail = envTrue(env.get(SettingsKeys.activityDetail))
        activityTrail = envTrue(env.get(SettingsKeys.activityTrail))
        contextNumber = envOn(env.get(SettingsKeys.contextNumber))
        rateBottomBar = envOn(env.get(SettingsKeys.rateBottomBar))
        rateReset = envOn(env.get(SettingsKeys.rateReset))
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
    }

    /// The six toggles that affect a single-session render (+ the connection's
    /// source colour), for GET /v1/preview. activity_trail is intentionally absent.
    public func draftDisplay(sourceColor: String) -> DraftDisplay {
        var d = DraftDisplay()
        d.contextPct = contextPct
        d.ratePct = ratePct
        d.activityDetail = activityDetail
        d.contextNumber = contextNumber
        d.rateBottomBar = rateBottomBar
        d.rateReset = rateReset
        d.sourceColor = sourceColor
        return d
    }
}
