import Foundation
import Observation

public struct KnobClaim: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var hwID: String
    public var shortID: String
    public var fw: String
    public var ip: String
    public var createdAt: Date
    public var expiresAt: Date
    public var knownDeviceID: String?

    enum CodingKeys: String, CodingKey {
        case id, fw, ip
        case hwID = "hw_id"
        case shortID = "short_id"
        case createdAt = "created_at"
        case expiresAt = "expires_at"
        case knownDeviceID = "known_device_id"
    }

    public init(id: String, hwID: String, shortID: String, fw: String = "", ip: String = "",
                createdAt: Date, expiresAt: Date, knownDeviceID: String? = nil) {
        self.id = id; self.hwID = hwID; self.shortID = shortID; self.fw = fw; self.ip = ip
        self.createdAt = createdAt; self.expiresAt = expiresAt; self.knownDeviceID = knownDeviceID
    }
}

struct KnobClaimList: Decodable {
    var claims: [KnobClaim]
}

public struct KnobPairingNotice: Equatable, Sendable, Identifiable {
    public let id: UUID
    public let knob: String
    public let failure: KnobPairingFailure
}

public enum KnobPairingFailure: Equatable, Sendable {
    case wrongCode(attemptsLeft: Int)
    case gone
    case alreadyApproved
    case alreadyPaired
    case other(FeedError)
}

public enum KnobPairingCode {
    public static func normalized(_ raw: String) -> String {
        raw.filter { !$0.isWhitespace }
    }

    public static func isValid(_ raw: String) -> Bool {
        let code = normalized(raw)
        return code.count == 6 && code.allSatisfy { $0.isASCII && $0.isNumber }
    }
}

@MainActor
@Observable
public final class KnobPairingModel {
    public private(set) var claims: [KnobClaim] = []
    public private(set) var isAvailable = true
    public private(set) var running: Set<String> = []
    public private(set) var failures: [String: KnobPairingFailure] = [:]
    public private(set) var notices: [KnobPairingNotice] = []

    public var isVisible: Bool { isAvailable && (!claims.isEmpty || !notices.isEmpty) }

    @ObservationIgnored public private(set) var service: KnobService
    @ObservationIgnored private var gateOpen = false
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var epoch = 0
    @ObservationIgnored private var refreshTask: Task<Void, Never>?
    @ObservationIgnored private let interval: Duration
    @ObservationIgnored private let noticeTTL: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void

    public static let pollEvery: Duration = .seconds(3)
    public static let noticeFor: Duration = .seconds(10)

    public convenience init(service: KnobService) {
        self.init(service: service, interval: Self.pollEvery, noticeTTL: Self.noticeFor,
                  sleep: { try await Task.sleep(for: $0) })
    }

    init(service: KnobService, interval: Duration, noticeTTL: Duration = KnobPairingModel.noticeFor,
         sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.service = service
        self.interval = interval
        self.noticeTTL = noticeTTL
        self.sleep = sleep
    }

    public func configure(service next: KnobService) {
        guard next != service else { return }
        service = next
        generation += 1
        claims = []; isAvailable = true; running = []; failures = [:]; notices = []; gateOpen = false
    }

    public func dismiss(_ notice: KnobPairingNotice) {
        notices.removeAll { $0.id == notice.id }
    }

    public func refresh() async {
        while let inFlight = refreshTask { await inFlight.value }
        let task = Task { @MainActor in
            await self.load()
            self.refreshTask = nil
        }
        refreshTask = task
        await task.value
    }

    private func load() async {
        let gen = generation, mark = epoch
        func current() -> Bool { gen == generation && mark == epoch }
        if !gateOpen {
            guard let info = try? await service.serverVersion(), gen == generation else { return }
            guard info.supports(VersionInfo.lanPair) else {
                isAvailable = false
                claims = []
                return
            }
            gateOpen = true
        }
        do {
            let list = try await service.claims()
            guard current() else { return }
            claims = list
            isAvailable = true
            failures = failures.filter { id, _ in list.contains { $0.id == id } || self.running.contains(id) }
        } catch let APIError.http(status, _) where status == 404 || status == 405 {
            guard gen == generation else { return }
            isAvailable = false
            claims = []
        } catch {}
    }

    public func poll() async {
        gateOpen = false
        isAvailable = true
        while !Task.isCancelled && isAvailable {
            await refresh()
            try? await sleep(interval)
        }
    }

    public func replaces(_ claim: KnobClaim, current: KnobDevice?) -> KnobDevice? {
        guard let current, current.hwID != claim.hwID else { return nil }
        return current
    }

    public func approve(_ claim: KnobClaim, code: String) async -> KnobDevice? {
        guard KnobPairingCode.isValid(code), !running.contains(claim.id) else { return nil }
        running.insert(claim.id)
        defer { running.remove(claim.id) }
        do {
            let device = try await service.approveClaim(id: claim.id, code: KnobPairingCode.normalized(code))
            epoch += 1
            failures[claim.id] = nil
            claims.removeAll { $0.id == claim.id }
            return device
        } catch let APIError.http(status, body) where status == 400 {
            epoch += 1
            if let left = Self.attemptsLeft(body) {
                if left > 0 { failures[claim.id] = .wrongCode(attemptsLeft: left) } else { close(claim, .wrongCode(attemptsLeft: 0)) }
            } else {
                failures[claim.id] = .other(FeedError(APIError.http(status: status, body: body)))
            }
        } catch let APIError.http(status, _) where status == 409 {
            close(claim, .alreadyPaired)
        } catch let APIError.http(status, _) where status == 404 {
            close(claim, .gone)
        } catch {
            failures[claim.id] = .other(FeedError(error))
        }
        await refresh()
        return nil
    }

    public func decline(_ claim: KnobClaim) async {
        guard !running.contains(claim.id) else { return }
        running.insert(claim.id)
        defer { running.remove(claim.id) }
        do {
            try await service.declineClaim(id: claim.id)
            epoch += 1
            failures[claim.id] = nil
            claims.removeAll { $0.id == claim.id }
        } catch let APIError.http(status, _) where status == 404 {
            epoch += 1
            claims.removeAll { $0.id == claim.id }
        } catch let APIError.http(status, _) where status == 409 {
            close(claim, .alreadyApproved)
            await refresh()
        } catch {
            failures[claim.id] = .other(FeedError(error))
        }
    }

    private func close(_ claim: KnobClaim, _ failure: KnobPairingFailure) {
        epoch += 1
        failures[claim.id] = nil
        claims.removeAll { $0.id == claim.id }
        let notice = KnobPairingNotice(id: UUID(), knob: claim.shortID, failure: failure)
        notices.append(notice)
        Task { @MainActor in
            try? await sleep(noticeTTL)
            dismiss(notice)
        }
    }

    private static func attemptsLeft(_ body: String) -> Int? {
        struct Rejected: Decodable { var error: String; var attemptsLeft: Int
            enum CodingKeys: String, CodingKey { case error; case attemptsLeft = "attempts_left" }
        }
        guard let r = try? JSONDecoder().decode(Rejected.self, from: Data(body.utf8)), r.error == "wrong_code" else { return nil }
        return r.attemptsLeft
    }
}
