import Foundation
import Observation

public enum KnobOTAAction: Hashable, Sendable {
    case update, install, retry, cancel, mode, upload, channel, delete, elf
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
    public private(set) var failedDeletes: [String: FeedError] = [:]
    public private(set) var keptDeletes: [String: KnobFirmwareKept] = [:]
    public private(set) var uploadProgress: Double?
    public private(set) var uploadStage: UploadStage?

    public enum UploadStage: Equatable, Sendable { case image, elf }

    @ObservationIgnored private(set) var service: KnobService
    @ObservationIgnored public private(set) var deviceID: String?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var statusSeq = 0
    @ObservationIgnored private var imagesSeq = 0

    public init(service: KnobService) { self.service = service }

    func configure(service next: KnobService, device: String?) {
        guard next != service || device != deviceID else { return }
        if next != service {
            images = []; imagesLoaded = false; unsupported = false
        }
        service = next
        deviceID = device
        generation += 1
        statusSeq += 1
        imagesSeq += 1
        running.remove(.delete)
        status = nil
        errors = [:]
        failedDeletes = [:]
        keptDeletes = [:]
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
        let gen = generation
        statusSeq += 1
        let seq = statusSeq
        do {
            let next = try await service.ota(id: id)
            guard gen == generation, seq == statusSeq else { return }
            status = next; unsupported = false
        } catch let e as APIError {
            guard gen == generation else { return }
            if case .http(404, _) = e, status == nil { unsupported = true }
        } catch {}
    }

    public func loadImages() async {
        let gen = generation
        imagesSeq += 1
        let seq = imagesSeq
        do {
            let list = try await service.firmware()
            guard gen == generation, seq == imagesSeq else { return }
            images = list
            imagesLoaded = true
            unsupported = false
        } catch let e as APIError {
            guard gen == generation else { return }
            if case .http(let code, _) = e, code == 404 || code == 503 { unsupported = true }
        } catch {}
    }

    private func apply(status next: KnobOTAStatus) {
        statusSeq += 1
        status = next
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

    public func deleteError(for image: KnobFirmwareImage) -> FeedError? {
        failedDeletes[image.version]
    }

    public func kept(_ image: KnobFirmwareImage) -> KnobFirmwareKept? {
        keptDeletes[image.version]
    }

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
        await put(.cancel, ["target": .null], map: KnobOTAError.cancelFailure)
    }

    @discardableResult
    public func setMode(_ mode: KnobOTAMode) async -> Bool {
        guard status?.mode != mode else { return true }
        return await put(.mode, ["mode": .string(mode.rawValue)])
    }

    private func put(_ action: KnobOTAAction, _ patch: [String: JSONValue],
                     map: (Error) -> FeedError = KnobOTAError.updateFailure) async -> Bool {
        guard let id = deviceID else { return false }
        let ok = await perform(action, map: map) {
            let next = try await self.service.updateOTA(id: id, patch: patch)
            if self.deviceID == id { self.apply(status: next) }
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
        await delete([image])
    }

    @discardableResult
    public func delete(_ images: [KnobFirmwareImage]) async -> Bool {
        guard !images.isEmpty else { return true }
        let gen = generation
        running.insert(.delete)
        defer { if gen == generation { running.remove(.delete) } }
        errors[.delete] = nil
        return await deleteEach(images, service: service, generation: gen, guarded: false, allowed: { _ in true })
    }

    @discardableResult
    public func deleteOldBuilds(_ confirmed: [KnobFirmwareImage]) async -> Bool {
        guard !confirmed.isEmpty, let id = deviceID else { return true }
        let service = service
        let gen = generation
        running.insert(.delete)
        defer { if gen == generation { running.remove(.delete) } }
        errors[.delete] = nil
        statusSeq += 1
        imagesSeq += 1
        let (sseq, iseq) = (statusSeq, imagesSeq)
        let otherKnobs: [String]
        do {
            let info = try await service.serverVersion()
            guard info.supports(VersionInfo.firmwareDeleteKeep) else {
                if gen == generation { errors[.delete] = .rejected(KnobOTAError.serverCantKeep) }
                return false
            }
            let next = try await service.ota(id: id)
            let list = try await service.firmware()
            let knobs = try await service.devices()
            guard gen == generation else { return false }
            if sseq == statusSeq { status = next }
            if iseq == imagesSeq { images = list }
            otherKnobs = Self.runningVersions(knobs)
        } catch {
            if gen == generation { errors[.delete] = FeedError(error) }
            return false
        }
        return await deleteEach(confirmed, service: service, generation: gen, guarded: true) { image in
            self.oldBuilds(otherKnobs: otherKnobs).contains { $0.version == image.version && $0.build == image.build }
        }
    }

    private func deleteEach(_ images: [KnobFirmwareImage], service: KnobService, generation gen: Int, guarded: Bool,
                            allowed: (KnobFirmwareImage) -> Bool) async -> Bool {
        failedDeletes = [:]
        keptDeletes = [:]
        for image in images {
            guard gen == generation else { return false }
            guard allowed(image) else { continue }
            do {
                do {
                    try await service.deleteFirmware(version: image.version, keepProtected: guarded)
                } catch APIError.http(404, _) {}
            } catch {
                guard gen == generation else { return false }
                if guarded, let kept = KnobFirmwareKept(error) {
                    keptDeletes[image.version] = kept
                } else {
                    failedDeletes[image.version] = KnobOTAError.deleteFailure(error)
                }
            }
        }
        guard gen == generation else { return false }
        await loadImages()
        await loadStatus()
        return failedDeletes.isEmpty
    }

    nonisolated public static func runningVersions(_ devices: [KnobDevice]) -> [String] {
        devices.filter { $0.kind == KnobDevice.knobKind }.compactMap(\.lastCheckin?.fw).filter { !$0.isEmpty }
    }

    public func oldBuilds(otherKnobs: [String]) -> [KnobFirmwareImage] {
        KnobFirmwareImage.oldBuilds(images, status: status, otherKnobs: otherKnobs)
    }

    public func badges(for image: KnobFirmwareImage) -> [KnobFirmwareBadge] {
        KnobFirmwareImage.badges(for: image, in: images, status: status)
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
