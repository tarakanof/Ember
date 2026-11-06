import Foundation

public enum KnobFirmwareSummary: Equatable, Sendable {
    case current
    case updateAvailable(String)
    case updating(percent: Int?)
    case failed(String)
}

extension KnobOTAStatus {
    public var summary: KnobFirmwareSummary {
        if isBusy { return .updating(percent: phase == .downloading ? progressPct : nil) }
        if phase.isFailure { return .failed(attemptVersion ?? "") }
        if let version = updateVersion { return .updateAvailable(version) }
        return .current
    }

    public static func expandsGroup(from old: KnobOTAStatus?, to new: KnobOTAStatus?) -> Bool {
        guard let old, let new else { return false }
        if new.isBusy { return !old.isBusy }
        if new.phase.isFailure {
            return !(old.phase.isFailure && old.attemptVersion == new.attemptVersion)
        }
        return false
    }
}
