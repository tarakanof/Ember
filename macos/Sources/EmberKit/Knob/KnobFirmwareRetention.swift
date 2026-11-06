import Foundation

public enum KnobFirmwareVersion {
    public static func compare(_ a: String, _ b: String) -> ComparisonResult {
        let (coreA, preA) = split(a)
        let (coreB, preB) = split(b)
        let pa = coreA.split(separator: ".", omittingEmptySubsequences: false)
        let pb = coreB.split(separator: ".", omittingEmptySubsequences: false)
        for (x, y) in zip(pa, pb) {
            let c = numeric(x, y)
            if c != .orderedSame { return c }
        }
        switch (preA.isEmpty, preB.isEmpty) {
        case (true, true): return .orderedSame
        case (true, false): return .orderedDescending
        case (false, true): return .orderedAscending
        case (false, false): return prerelease(preA, preB)
        }
    }

    public static func isNewer(_ a: String, than b: String) -> Bool {
        compare(a, b) == .orderedDescending
    }

    private static func split(_ v: String) -> (Substring, Substring) {
        guard let dash = v.firstIndex(of: "-") else { return (Substring(v), "") }
        return (v[..<dash], v[v.index(after: dash)...])
    }

    private static func order<T: Comparable>(_ a: T, _ b: T) -> ComparisonResult {
        a == b ? .orderedSame : (a < b ? .orderedAscending : .orderedDescending)
    }

    private static func numeric(_ a: Substring, _ b: Substring) -> ComparisonResult {
        a.count != b.count ? order(a.count, b.count) : order(a, b)
    }

    private static func unsigned(_ s: Substring) -> UInt64? {
        guard !s.isEmpty, s.allSatisfy({ $0.isASCII && $0.isNumber }) else { return nil }
        return UInt64(s)
    }

    private static func prerelease(_ a: Substring, _ b: Substring) -> ComparisonResult {
        let ia = a.split(separator: ".", omittingEmptySubsequences: false)
        let ib = b.split(separator: ".", omittingEmptySubsequences: false)
        for (x, y) in zip(ia, ib) {
            let c: ComparisonResult
            switch (unsigned(x), unsigned(y)) {
            case let (nx?, ny?): c = order(nx, ny)
            case (_?, nil): c = .orderedAscending
            case (nil, _?): c = .orderedDescending
            case (nil, nil): c = order(x, y)
            }
            if c != .orderedSame { return c }
        }
        return order(ia.count, ib.count)
    }
}

public enum KnobFirmwareBadge: Hashable, Sendable, CaseIterable {
    case onKnob, latest, installing, failed, noELF
}

extension KnobFirmwareImage {
    public static func newest(_ images: some Sequence<KnobFirmwareImage>) -> KnobFirmwareImage? {
        images.max { KnobFirmwareVersion.compare($0.version, $1.version) == .orderedAscending }
    }

    public static func oldBuilds(_ images: [KnobFirmwareImage], status: KnobOTAStatus?,
                                 otherKnobs: [String] = []) -> [KnobFirmwareImage] {
        guard let status, let running = status.running else { return [] }
        var keep = Set(otherKnobs)
        keep.insert(running.fw)
        let release = newest(images.filter(\.isRelease))
        let test = newest(images.filter { !$0.isRelease })
        if let release { keep.insert(release.version) }
        if let test, release.map({ KnobFirmwareVersion.isNewer(test.version, than: $0.version) }) ?? true {
            keep.insert(test.version)
        }
        let pending = [status.target, status.isBusy ? status.version : nil, status.updateVersion]
        for version in pending.compactMap({ $0 }) {
            keep.insert(version)
        }
        return images.filter { !status.runs($0) && !keep.contains($0.version) }
    }

    public static func badges(for image: KnobFirmwareImage, in images: [KnobFirmwareImage],
                              status: KnobOTAStatus?) -> [KnobFirmwareBadge] {
        var out: [KnobFirmwareBadge] = []
        if status?.runs(image) == true { out.append(.onKnob) }
        if newest(images)?.version == image.version { out.append(.latest) }
        if let status, status.attemptVersion == image.version {
            if status.phase.isInProgress, status.phase != .offered { out.append(.installing) }
            if status.phase.isFailure { out.append(.failed) }
        }
        if !image.elf { out.append(.noELF) }
        return out
    }
}
