import CryptoKit
import Foundation

/// The key launch compares to decide whether the app was updated since the
/// producer LaunchAgents were last re-registered (`shouldReconcileAfterUpdate`).
public func producerBundleFingerprint(version: String, build: String, helperDigests: [String]) -> String {
    "\(version) (\(build)) " + helperDigests.joined(separator: ",")
}

/// `producerBundleFingerprint` for the app at `appURL`: a SHA-256 prefix of
/// each producer binary in `Contents/MacOS` and LaunchAgent plist in
/// `Contents/Library/LaunchAgents`, in `ProducerAgent` order.
public func bundleFingerprint(appURL: URL, version: String, build: String) -> String {
    let contents = appURL.appendingPathComponent("Contents")
    let files = ProducerAgent.allCases.flatMap { agent in
        [contents.appendingPathComponent("MacOS/\(agent.binaryName)"),
         contents.appendingPathComponent("Library/LaunchAgents/\(agent.plistName)")]
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
