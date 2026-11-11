import Foundation
import Security

public enum BuiltInServer {
    public static let label = "com.ember.server"
    public static let binaryName = "ember-server"
    public static let port = 3627
    public static let localURL = "http://127.0.0.1:3627"
}

public enum ServerMode: String, Sendable, Equatable, CaseIterable {
    case thisMac
    case anotherServer

    public init(registration: AgentRegistration) {
        switch registration {
        case .enabled, .requiresApproval: self = .thisMac
        case .notRegistered, .notFound: self = .anotherServer
        }
    }
}

public enum ServerJobState: Sendable, Equatable {
    case running
    case waiting
    case notLoaded
    case stuck
    case unknown
}

public func serverJobState(_ result: CommandResult) -> ServerJobState {
    switch launchdProbe(result) {
    case .loaded: launchctlPrintFields(result.stdout)["state"] == "running" ? .running : .waiting
    case .notLoaded: .notLoaded
    case .stuck: .stuck
    case .unknown: .unknown
    }
}

public enum ServerHealth: Sendable, Equatable {
    case ok
    case closed
    case occupied
}

public enum BuiltInServerStatus: Sendable, Equatable {
    case off
    case starting
    case running(version: String?)
    case notRunning
    case portInUse
    case needsApproval
    case missing
}

public func builtInServerStatus(registration: AgentRegistration, job: ServerJobState,
                                health: ServerHealth, version: String?) -> BuiltInServerStatus {
    switch registration {
    case .notRegistered: return .off
    case .requiresApproval: return .needsApproval
    case .notFound: return .missing
    case .enabled: break
    }
    switch (job, health) {
    case (.running, .ok), (.unknown, .ok): return .running(version: version)
    case (.running, _), (.unknown, .occupied): return .starting
    case (_, .ok), (_, .occupied): return .portInUse
    case (_, .closed): return .notRunning
    }
}

public struct BuiltInServerPaths: Sendable, Equatable {
    public let serverEnv: URL
    public let producerEnv: URL
    public let dataDir: URL
    public let log: URL

    public init(home: URL) {
        serverEnv = home.appendingPathComponent(".config/ember/server.env")
        producerEnv = home.appendingPathComponent(".config/ember/producer.env")
        dataDir = home.appendingPathComponent("Library/Application Support/Ember")
        log = home.appendingPathComponent("Library/Logs/Ember/server.log")
    }
}

public enum BuiltInServerError: Error, Equatable, Sendable {
    case tokenUnavailable(OSStatus)
    case portInUse
}

extension BuiltInServerError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .tokenUnavailable(let status):
            String(localized: "macOS couldn't generate a server token (error \(status)).",
                   comment: "Settings › Connection failure when SecRandomCopyBytes fails while turning on the built-in server; the OSStatus code.")
        case .portInUse:
            String(localized: "Another program is using port \(String(BuiltInServer.port)), so the server on this Mac didn't start. Ember kept your previous server.",
                   comment: "Settings › Connection failure when turning on the built-in server finds its port taken; the port number.")
        }
    }
}

public func mintServerToken() throws -> String {
    var bytes = [UInt8](repeating: 0, count: 32)
    let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
    guard status == errSecSuccess else { throw BuiltInServerError.tokenUnavailable(status) }
    return bytes.map { String(format: "%02x", $0) }.joined()
}

public protocol BuiltInServerProbing: Sendable {
    func health() async -> ServerHealth
    func version() async -> String?
}

public struct LoopbackServerProbe: BuiltInServerProbing {
    private let session: URLSession
    private let base: URL

    public init(base: URL = URL(string: BuiltInServer.localURL)!) {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 2
        config.timeoutIntervalForResource = 2
        config.connectionProxyDictionary = [:]
        config.waitsForConnectivity = false
        session = URLSession(configuration: config)
        self.base = base
    }

    public func health() async -> ServerHealth {
        let request = URLRequest(url: base.appendingPathComponent("healthz"))
        do {
            let (_, response) = try await session.data(for: request)
            return (response as? HTTPURLResponse)?.statusCode == 200 ? .ok : .occupied
        } catch let error as URLError where Self.closedCodes.contains(error.code) {
            return .closed
        } catch {
            return .occupied
        }
    }

    private static let closedCodes: Set<URLError.Code> = [
        .cannotConnectToHost, .cannotFindHost, .networkConnectionLost, .notConnectedToInternet,
    ]

    public func version() async -> String? {
        let client = APIClient(baseURL: base, token: nil, session: session)
        let info: VersionInfo? = try? await client.get("/version")
        return info?.short
    }
}

public final class BuiltInServerService: Sendable {
    private let jobs: ProducerInstallService
    private let probe: BuiltInServerProbing
    public let paths: BuiltInServerPaths
    private let producerEnv: EnvFileStore
    private let serverEnv: EnvFileStore
    private let mintToken: @Sendable () throws -> String
    private let pause: @Sendable () async -> Void
    private let startAttempts: Int
    private let stopAttempts: Int

    public init(jobs: ProducerInstallService,
                probe: BuiltInServerProbing = LoopbackServerProbe(),
                paths: BuiltInServerPaths,
                producerEnv: EnvFileStore,
                mintToken: @escaping @Sendable () throws -> String = mintServerToken,
                pause: @escaping @Sendable () async -> Void = { try? await Task.sleep(for: .milliseconds(500)) },
                startAttempts: Int = 30,
                stopAttempts: Int = 20) {
        self.jobs = jobs
        self.probe = probe
        self.paths = paths
        self.producerEnv = producerEnv
        serverEnv = EnvFileStore(path: paths.serverEnv)
        self.mintToken = mintToken
        self.pause = pause
        self.startAttempts = startAttempts
        self.stopAttempts = stopAttempts
    }

    public func mode() -> ServerMode {
        ServerMode(registration: jobs.serverRegistration())
    }

    @concurrent
    public func status() async -> BuiltInServerStatus {
        let registration = jobs.serverRegistration()
        guard registration == .enabled else {
            return builtInServerStatus(registration: registration, job: .unknown, health: .closed, version: nil)
        }
        let job = jobs.serverJob()
        let health = await probe.health()
        var version: String?
        if health == .ok, job == .running || job == .unknown {
            version = await probe.version()
        }
        return builtInServerStatus(registration: registration, job: job, health: health, version: version)
    }

    @concurrent
    public func turnOn(progress: @escaping @Sendable (BuiltInServerStatus) async -> Void = { _ in }) async throws
        -> BuiltInServerStatus {
        let token = try prepareServerFiles()
        if let error = await jobs.registerServer() { throw error }
        try await pointProducers(at: token)
        let final = await waitForStart(progress: progress)
        guard final == .portInUse else { return final }
        _ = try await turnOff()
        throw BuiltInServerError.portInUse
    }

    @concurrent
    public func start(progress: @escaping @Sendable (BuiltInServerStatus) async -> Void = { _ in }) async throws
        -> BuiltInServerStatus {
        if let error = await jobs.healServer() { throw error }
        return await waitForStart(progress: progress)
    }

    @concurrent
    public func turnOff() async throws -> BuiltInServerStatus {
        if let error = await jobs.unregisterServer() { throw error }
        for attempt in 0..<stopAttempts {
            if await probe.health() == .closed { break }
            if attempt < stopAttempts - 1 { await pause() }
        }
        let token = await serverEnv.read().get(SettingsKeys.token)
        try await producerEnv.update { env in restoreRemote(in: &env, serverToken: token) }
        return await status()
    }

    private func prepareServerFiles() throws -> String {
        let fm = FileManager.default
        try fm.createDirectory(at: paths.dataDir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        var env = EnvFile(parsing: (try? String(contentsOf: paths.serverEnv, encoding: .utf8)) ?? "")
        let existing = env.get(SettingsKeys.token)
        if !existing.isEmpty { return existing }
        let token = try mintToken()
        env.set(SettingsKeys.token, token)
        try env.write(to: paths.serverEnv)
        return token
    }

    private func pointProducers(at token: String) async throws {
        try await producerEnv.update { env in pointAtBuiltIn(&env, token: token) }
    }

    private func waitForStart(progress: @Sendable (BuiltInServerStatus) async -> Void) async -> BuiltInServerStatus {
        var last = BuiltInServerStatus.starting
        await progress(last)
        for attempt in 0..<startAttempts {
            last = await status()
            switch last {
            case .running, .needsApproval, .missing, .off: return last
            case .starting, .notRunning, .portInUse: break
            }
            if attempt < startAttempts - 1 { await pause() }
        }
        return last == .starting ? .notRunning : last
    }
}

public func pointAtBuiltIn(_ env: inout EnvFile, token: String) {
    let currentURL = env.get(SettingsKeys.serverURL)
    if currentURL != BuiltInServer.localURL {
        env.set(SettingsKeys.previousServerURL, currentURL)
        env.set(SettingsKeys.previousToken, env.get(SettingsKeys.token))
    }
    env.set(SettingsKeys.serverURL, BuiltInServer.localURL)
    env.set(SettingsKeys.token, token)
}

public func restoreRemote(in env: inout EnvFile, serverToken: String) {
    if env.contains(SettingsKeys.previousServerURL) {
        restore(SettingsKeys.serverURL, to: env.get(SettingsKeys.previousServerURL), in: &env)
        restore(SettingsKeys.token, to: env.get(SettingsKeys.previousToken), in: &env)
        env.remove(SettingsKeys.previousServerURL)
        env.remove(SettingsKeys.previousToken)
        return
    }
    if env.get(SettingsKeys.serverURL) == BuiltInServer.localURL { env.remove(SettingsKeys.serverURL) }
    if !serverToken.isEmpty, env.get(SettingsKeys.token) == serverToken { env.remove(SettingsKeys.token) }
}

private func restore(_ key: String, to value: String, in env: inout EnvFile) {
    if value.isEmpty { env.remove(key) } else { env.set(key, value) }
}
