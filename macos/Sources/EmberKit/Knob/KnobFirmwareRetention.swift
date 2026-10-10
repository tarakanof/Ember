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
        if pa.count != pb.count { return pa.count < pb.count ? .orderedAscending : .orderedDescending }
        switch (preA, preB) {
        case (nil, nil): return .orderedSame
        case (nil, _): return .orderedDescending
        case (_, nil): return .orderedAscending
        case let (x?, y?): return prerelease(x, y)
        }
    }

    public static func isNewer(_ a: String, than b: String) -> Bool {
        compare(a, b) == .orderedDescending
    }

    private static func split(_ v: String) -> (Substring, Substring?) {
        guard let dash = v.firstIndex(of: "-") else { return (Substring(v), nil) }
        return (v[..<dash], v[v.index(after: dash)...])
    }

    private static func numeric(_ a: Substring, _ b: Substring) -> ComparisonResult {
        if a.count != b.count { return a.count < b.count ? .orderedAscending : .orderedDescending }
        return a == b ? .orderedSame : (a < b ? .orderedAscending : .orderedDescending)
    }

    private static func prerelease(_ a: Substring, _ b: Substring) -> ComparisonResult {
        let ia = a.split(separator: ".", omittingEmptySubsequences: false)
        let ib = b.split(separator: ".", omittingEmptySubsequences: false)
        for (x, y) in zip(ia, ib) {
            let c: ComparisonResult
            switch (UInt64(x), UInt64(y)) {
            case let (nx?, ny?): c = nx == ny ? .orderedSame : (nx < ny ? .orderedAscending : .orderedDescending)
            case (_?, nil): c = .orderedAscending
            case (nil, _?): c = .orderedDescending
            case (nil, nil): c = x == y ? .orderedSame : (x < y ? .orderedAscending : .orderedDescending)
            }
            if c != .orderedSame { return c }
        }
        if ia.count == ib.count { return .orderedSame }
        return ia.count < ib.count ? .orderedAscending : .orderedDescending
    }
}

public enum KnobFirmwareBadge: Hashable, Sendable, CaseIterable {
    case onKnob, latest, installing, failed, noELF
}

extension KnobFirmwareImage {
    public static func newest(_ images: some Sequence<KnobFirmwareImage>) -> KnobFirmwareImage? {
        images.max { KnobFirmwareVersion.compare($0.version, $1.version) == .orderedAscending }
    }

    public static func oldBuilds(_ images: [KnobFirmwareImage], status: KnobOTAStatus?) -> [KnobFirmwareImage] {
        guard let status, status.running != nil else { return [] }
        var keep = Set<String>()
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
            if status.isBusy { out.append(.installing) }
            if status.phase.isFailure { out.append(.failed) }
        }
        if !image.elf { out.append(.noELF) }
        return out
    }
}
