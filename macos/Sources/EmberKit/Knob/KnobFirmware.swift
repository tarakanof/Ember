import Foundation

public struct KnobFirmwareImage: Codable, Equatable, Sendable, Identifiable {
    public static let release = "release"
    public static let test = "test"

    public var build: String
    public var channel: String
    public var elf: Bool
    public var idfVer: String
    public var project: String
    public var sha256: String
    public var size: Int
    public var uploadedAt: Date
    public var version: String

    public var id: String { version }
    public var isRelease: Bool { channel == Self.release }

    enum CodingKeys: String, CodingKey {
        case build, channel, elf, project, sha256, size, version
        case idfVer = "idf_ver"
        case uploadedAt = "uploaded_at"
    }

    public init(build: String, channel: String, elf: Bool, idfVer: String, project: String = "cinder",
                sha256: String, size: Int, uploadedAt: Date, version: String) {
        self.build = build; self.channel = channel; self.elf = elf; self.idfVer = idfVer
        self.project = project; self.sha256 = sha256; self.size = size
        self.uploadedAt = uploadedAt; self.version = version
    }

    public var elfFilename: String { "cinder-\(version).elf" }

    public static func needsConfirmation(from: String, to: String) -> Bool {
        from != release && to == release
    }
}

public enum KnobOTAMode: String, Codable, Sendable, CaseIterable {
    case manual, auto
}

public enum KnobOTAPhase: String, Codable, Sendable {
    case idle, offered, downloading, installing, restarting, verifying, done, failed
    case rolledBack = "rolled_back"

    public init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = Self(rawValue: raw) ?? .idle
    }

    public var isInProgress: Bool {
        switch self {
        case .offered, .downloading, .installing, .restarting, .verifying: true
        case .idle, .done, .failed, .rolledBack: false
        }
    }

    public var isFailure: Bool { self == .failed || self == .rolledBack }

    public var pollsFast: Bool { self == .downloading || self == .installing || self == .restarting }
}

public enum KnobOTAWait: String, Codable, Sendable {
    case pomodoro, coredump
    case idleInput = "idle_input"
}

public struct KnobOTAStatus: Codable, Equatable, Sendable {
    public struct Running: Codable, Equatable, Sendable {
        public var fw: String
        public var build: String?
        public var slot: Int?
        public var image: String?
        public var rollback: Bool

        public init(fw: String, build: String? = nil, slot: Int? = nil, image: String? = nil, rollback: Bool) {
            self.fw = fw; self.build = build; self.slot = slot; self.image = image; self.rollback = rollback
        }
    }

    public var mode: KnobOTAMode
    public var target: String?
    public var version: String?
    public var phase: KnobOTAPhase
    public var progressPct: Int?
    public var bytes: Int?
    public var size: Int?
    public var from: String?
    public var error: String?
    public var startedAt: Date?
    public var finishedAt: Date?
    public var blocked: [String]
    public var running: Running?
    public var available: String?
    public var waitingFor: KnobOTAWait?

    enum CodingKeys: String, CodingKey {
        case mode, target, version, phase, bytes, size, from, error, blocked, running, available
        case progressPct = "progress_pct"
        case startedAt = "started_at"
        case finishedAt = "finished_at"
        case waitingFor = "waiting_for"
    }

    public init(mode: KnobOTAMode = .manual, target: String? = nil, phase: KnobOTAPhase = .idle,
                progressPct: Int? = nil, bytes: Int? = nil, size: Int? = nil, from: String? = nil,
                error: String? = nil, startedAt: Date? = nil, finishedAt: Date? = nil, blocked: [String] = [],
                running: Running? = nil, available: String? = nil, waitingFor: KnobOTAWait? = nil,
                version: String? = nil) {
        self.mode = mode; self.target = target; self.version = version; self.phase = phase; self.progressPct = progressPct
        self.bytes = bytes; self.size = size; self.from = from; self.error = error
        self.startedAt = startedAt; self.finishedAt = finishedAt; self.blocked = blocked
        self.running = running; self.available = available; self.waitingFor = waitingFor
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mode = (try? c.decode(KnobOTAMode.self, forKey: .mode)) ?? .manual
        target = try c.decodeIfPresent(String.self, forKey: .target)
        version = try c.decodeIfPresent(String.self, forKey: .version)
        phase = try c.decodeIfPresent(KnobOTAPhase.self, forKey: .phase) ?? .idle
        progressPct = try c.decodeIfPresent(Int.self, forKey: .progressPct)
        bytes = try c.decodeIfPresent(Int.self, forKey: .bytes)
        size = try c.decodeIfPresent(Int.self, forKey: .size)
        from = try c.decodeIfPresent(String.self, forKey: .from)
        error = try c.decodeIfPresent(String.self, forKey: .error)
        startedAt = try c.decodeIfPresent(Date.self, forKey: .startedAt)
        finishedAt = try c.decodeIfPresent(Date.self, forKey: .finishedAt)
        blocked = try c.decodeIfPresent([String].self, forKey: .blocked) ?? []
        running = try c.decodeIfPresent(Running.self, forKey: .running)
        available = try c.decodeIfPresent(String.self, forKey: .available)
        waitingFor = try? c.decodeIfPresent(KnobOTAWait.self, forKey: .waitingFor)
    }

    public var canRollBack: Bool { running?.rollback == true }

    public var isBusy: Bool { phase.isInProgress || (target != nil && phase == .idle) }

    public var progress: Double? {
        guard let pct = progressPct else { return nil }
        return Double(min(max(pct, 0), 100)) / 100
    }

    public var attemptVersion: String? { version ?? target }

    public func runs(_ image: KnobFirmwareImage) -> Bool {
        guard let running else { return false }
        if let build = running.build, !build.isEmpty { return image.build == build }
        return image.version == running.fw
    }

    public func canInstall(_ image: KnobFirmwareImage) -> Bool {
        running != nil && !isBusy && !runs(image)
    }

    public var canCancel: Bool { target != nil && (phase == .idle || phase == .offered) }

    public var updateVersion: String? {
        guard let available, !isBusy else { return nil }
        if phase.isFailure, available == attemptVersion { return nil }
        return available
    }
}

public enum KnobOTAError {
    public static let inProgress = LocalizedStringResource("The knob is already installing an update. Try again when it has finished.")
    public static let noRollback = LocalizedStringResource("This knob's bootloader can't roll back. Flash it once over USB (see cinder docs/workflow.md).")
    public static let unknownImage = LocalizedStringResource("Ember no longer stores this image.")
    public static let inUse = LocalizedStringResource("A knob is set to update to this version. Cancel that update in the Firmware row, or wait until it has finished, then delete again.")

    public static func deleteFailure(_ error: Error) -> FeedError {
        if case .http(409, let body)? = error as? APIError, body.contains("update target") {
            return .rejected(inUse)
        }
        return FeedError(error)
    }

    public static func updateFailure(_ error: Error) -> FeedError {
        if case .http(let status, let body)? = error as? APIError {
            switch status {
            case 409 where body.contains("ota_in_progress"): return .rejected(inProgress)
            case 409 where body.contains("no_rollback_bootloader"): return .rejected(noRollback)
            case 400 where body.contains("unknown firmware version"): return .rejected(unknownImage)
            default: break
            }
        }
        return FeedError(error)
    }

    public static func label(_ code: String) -> String {
        switch code {
        case "no_checkin": String(localized: "the knob could not reach Ember after the update")
        case "boot": String(localized: "the new firmware restarted before it was confirmed")
        case "net": String(localized: "the download failed")
        case "size": String(localized: "the download had the wrong size")
        case "sha256": String(localized: "the download did not match its checksum")
        case "image": String(localized: "the image failed its check")
        case "desc": String(localized: "the image does not fit this knob")
        case "flash": String(localized: "writing the flash failed")
        case "interrupted": String(localized: "the knob restarted during the download")
        case "mark_valid": String(localized: "the knob could not confirm the new firmware")
        case "refused": String(localized: "the knob refused this image after it failed there before")
        case "not_started": String(localized: "the knob never started the download")
        case "reset_waiting": String(localized: "the knob restarted while the update was waiting to install")
        case "health_display": String(localized: "the display link check did not pass after the update")
        case "health_render": String(localized: "the screen did not draw after the update")
        case "health_heap": String(localized: "memory ran low after the update")
        case "health_stack": String(localized: "a task ran low on stack after the update")
        default:
            if let status = code.split(separator: "_").last, code.hasPrefix("http_") {
                String(localized: "Ember answered HTTP \(String(status))",
                       comment: "Knob firmware update error: the download got an HTTP error status from Ember (\"Ember answered HTTP 409\").")
            } else {
                code
            }
        }
    }
}
