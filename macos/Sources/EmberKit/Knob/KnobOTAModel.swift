import Foundation
import Observation

public enum KnobOTAAction: Hashable, Sendable {
    case update, install, retry, mode, upload, channel, delete, elf
}

@MainActor
@Observable
public final class KnobOTAModel {
    public private(set) var status: KnobOTAStatus?
    public private(set) var images: [KnobFirmwareImage] = []
    public private(set) var imagesLoaded = false
    public private(set) var unsupported = false
    public private(set) var running: Set<KnobOTAAction> = []
    public private(set) var errors: [KnobOTAAction: FeedError] = [:]
    public private(set) var uploadProgress: Double?
    public private(set) var uploadStage: UploadStage?

    public enum UploadStage: Equatable, Sendable { case image, elf }

    @ObservationIgnored private(set) var service: KnobService
    @ObservationIgnored public private(set) var deviceID: String?

    public init(service: KnobService) { self.service = service }

    func configure(service next: KnobService, device: String?) {
        guard next != service || device != deviceID else { return }
        if next != service {
            images = []; imagesLoaded = false; unsupported = false
        }
        service = next
        deviceID = device
        status = nil
        errors = [:]
    }

    public var pollInterval: Duration {
        status?.phase.pollsFast == true ? .seconds(1) : .seconds(15)
    }

    public func followProgress(sleep: (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
                               finished: () async -> Void) async {
        while !Task.isCancelled {
            do { try await sleep(pollInterval) } catch { return }
            guard status?.phase.pollsFast == true else { continue }
            await loadStatus()
            if status?.phase.pollsFast == false { await finished() }
        }
    }

    public func loadStatus() async {
        guard let id = deviceID else { status = nil; return }
        do {
            let next = try await service.ota(id: id)
            if deviceID == id { status = next; unsupported = false }
        } catch let e as APIError {
            if case .http(404, _) = e, status == nil { unsupported = true }
        } catch {}
    }

    public func loadImages() async {
        do {
            images = try await service.firmware()
            imagesLoaded = true
            unsupported = false
        } catch let e as APIError {
            if case .http(let code, _) = e, code == 404 || code == 503 { unsupported = true }
        } catch {}
    }

    @discardableResult
    func perform(_ action: KnobOTAAction, map: (Error) -> FeedError = { FeedError($0) },
                 _ body: () async throws -> Void) async -> Bool {
        running.insert(action)
        defer { running.remove(action) }
        do {
            try await body()
            errors[action] = nil
            return true
        } catch {
            errors[action] = map(error)
            return false
        }
    }

    public func clearError(_ action: KnobOTAAction) { errors[action] = nil }

    @discardableResult
    public func update(to version: String) async -> Bool {
        await put(.update, ["target": .string(version)])
    }

    @discardableResult
    public func retry() async -> Bool {
        await put(.retry, ["retry": .bool(true)])
    }

    @discardableResult
    public func cancel() async -> Bool {
        await put(.update, ["target": .null])
    }

    @discardableResult
    public func setMode(_ mode: KnobOTAMode) async -> Bool {
        guard status?.mode != mode else { return true }
        return await put(.mode, ["mode": .string(mode.rawValue)])
    }

    private func put(_ action: KnobOTAAction, _ patch: [String: JSONValue]) async -> Bool {
        guard let id = deviceID else { return false }
        let ok = await perform(action, map: KnobOTAError.updateFailure) {
            let next = try await self.service.updateOTA(id: id, patch: patch)
            if self.deviceID == id { self.status = next }
        }
        if !ok { await loadStatus() }
        return ok
    }

    nonisolated public static func elfURL(besides binary: URL, exists: (URL) -> Bool = { FileManager.default.fileExists(atPath: $0.path) }) -> URL? {
        let dir = binary.deletingLastPathComponent()
        let candidates = [binary.deletingPathExtension().appendingPathExtension("elf"),
                          dir.appendingPathComponent("cinder.elf")]
        return candidates.first(where: exists)
    }

    @discardableResult
    public func upload(binary: URL, channel: String, elf: URL? = nil) async -> KnobFirmwareImage? {
        var stored: KnobFirmwareImage?
        uploadProgress = 0
        uploadStage = .image
        defer { uploadProgress = nil; uploadStage = nil }
        let ok = await perform(.upload) {
            let data = try Data(contentsOf: binary)
            let image = try await self.service.uploadFirmware(data, channel: channel) { p in
                Task { @MainActor in self.uploadProgress = p }
            }
            stored = image
            if let elf, !image.elf {
                self.uploadStage = .elf
                self.uploadProgress = 0
                let elfData = try Data(contentsOf: elf)
                try await self.service.uploadELF(version: image.version, elfData) { p in
                    Task { @MainActor in self.uploadProgress = p }
                }
            }
        }
        await loadImages()
        await loadStatus()
        return ok ? stored : nil
    }

    @discardableResult
    public func setChannel(_ image: KnobFirmwareImage, to channel: String) async -> Bool {
        guard channel == KnobFirmwareImage.release || channel == KnobFirmwareImage.test else { return false }
        guard channel != image.channel else { return true }
        let ok = await perform(.channel) {
            _ = try await self.service.setFirmwareChannel(version: image.version, channel: channel)
        }
        await loadImages()
        return ok
    }

    @discardableResult
    public func install(_ image: KnobFirmwareImage) async -> Bool {
        await put(.install, ["target": .string(image.version)])
    }

    @discardableResult
    public func delete(_ image: KnobFirmwareImage) async -> Bool {
        let ok = await perform(.delete) {
            do {
                try await self.service.deleteFirmware(version: image.version)
            } catch APIError.http(404, _) {}
        }
        await loadImages()
        await loadStatus()
        return ok
    }

    public func elfData(version: String) async -> Data? {
        var data: Data?
        await perform(.elf) { data = try await self.service.firmwareELF(version: version) }
        return data
    }

    public func elfData(build: String) async -> Data? {
        var data: Data?
        await perform(.elf) { data = try await self.service.firmwareELF(build: build) }
        return data
    }

    public func runsOnKnob(_ image: KnobFirmwareImage) -> Bool {
        status?.runs(image) == true
    }

    public func elfImage(build: String?) -> KnobFirmwareImage? {
        guard let build, !build.isEmpty else { return nil }
        return images.first { $0.build == build && $0.elf }
    }
}
