import CryptoKit
import Foundation

public func producerBundleFingerprint(version: String, build: String, helperDigests: [String]) -> String {
    "\(version) (\(build)) " + helperDigests.joined(separator: ",")
}

public func bundleFingerprint(appURL: URL, version: String, build: String) -> String {
    let contents = appURL.appendingPathComponent("Contents")
    let helpers = ProducerAgent.allCases.map { (binary: $0.binaryName, plist: $0.plistName) }
        + [(binary: BuiltInServer.binaryName, plist: ManagedJob.server.plistName)]
    let files = helpers.flatMap { helper in
        [contents.appendingPathComponent("MacOS/\(helper.binary)"),
         contents.appendingPathComponent("Library/LaunchAgents/\(helper.plist)")]
    }
    return producerBundleFingerprint(version: version, build: build, helperDigests: files.map(fileDigest))
}

func fileDigest(_ url: URL) -> String {
    guard let handle = try? FileHandle(forReadingFrom: url) else { return "missing" }
    defer { try? handle.close() }
    var hasher = SHA256()
    while let chunk = try? handle.read(upToCount: 1 << 20), !chunk.isEmpty {
        hasher.update(data: chunk)
    }
    return hasher.finalize().prefix(8).map { String(format: "%02x", $0) }.joined()
}
