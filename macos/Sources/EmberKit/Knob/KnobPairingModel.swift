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

    @ObservationIgnored public private(set) var service: KnobService
    @ObservationIgnored private var gateOpen = false
    @ObservationIgnored private let interval: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void

    public static let pollEvery: Duration = .seconds(3)

    public convenience init(service: KnobService) {
        self.init(service: service, interval: Self.pollEvery, sleep: { try await Task.sleep(for: $0) })
    }

    init(service: KnobService, interval: Duration, sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.service = service
        self.interval = interval
        self.sleep = sleep
    }

    public func configure(service next: KnobService) {
        guard next != service else { return }
        service = next
        claims = []; isAvailable = true; running = []; failures = [:]; gateOpen = false
    }

    public func refresh() async {
        if !gateOpen {
            guard let info = try? await service.serverVersion() else { return }
            guard info.supports(VersionInfo.lanPair) else {
                isAvailable = false
                claims = []
                return
            }
            gateOpen = true
        }
        do {
            let list = try await service.claims()
            claims = list
            isAvailable = true
            failures = failures.filter { id, _ in list.contains { $0.id == id } || self.running.contains(id) }
        } catch let APIError.http(status, _) where status == 404 || status == 405 {
            isAvailable = false
            claims = []
        } catch {}
    }

    public func poll() async {
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
            failures[claim.id] = nil
            claims.removeAll { $0.id == claim.id }
            return device
        } catch let APIError.http(status, body) where status == 400 {
            failures[claim.id] = Self.attemptsLeft(body).map { .wrongCode(attemptsLeft: $0) } ?? .other(FeedError(APIError.http(status: status, body: body)))
            if case .wrongCode(0)? = failures[claim.id] { await refresh() }
        } catch let APIError.http(status, _) where status == 409 {
            failures[claim.id] = .alreadyPaired
            await refresh()
        } catch let APIError.http(status, _) where status == 404 {
            failures[claim.id] = .gone
            await refresh()
        } catch {
            failures[claim.id] = .other(FeedError(error))
        }
        return nil
    }

    public func decline(_ claim: KnobClaim) async {
        guard !running.contains(claim.id) else { return }
        running.insert(claim.id)
        defer { running.remove(claim.id) }
        do {
            try await service.declineClaim(id: claim.id)
            failures[claim.id] = nil
            claims.removeAll { $0.id == claim.id }
        } catch let APIError.http(status, _) where status == 404 {
            claims.removeAll { $0.id == claim.id }
        } catch let APIError.http(status, _) where status == 409 {
            failures[claim.id] = .alreadyApproved
            await refresh()
        } catch {
            failures[claim.id] = .other(FeedError(error))
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
