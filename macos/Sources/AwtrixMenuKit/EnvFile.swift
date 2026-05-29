import Foundation

/// Parsed producer.env preserving original lines/comments/order. get/set use
/// last-write-wins to match the producer's map parser.
public struct EnvFile: Sendable {
    private struct Line { var raw: String; var key: String?; var value: String }
    private var lines: [Line]

    public init(parsing text: String) {
        var out: [Line] = []
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init) {
            let stripped = raw.trimmingCharacters(in: .whitespaces)
            if stripped.isEmpty || stripped.hasPrefix("#") { out.append(Line(raw: raw, key: nil, value: "")); continue }
            guard let eq = raw.firstIndex(of: "="), eq != raw.startIndex else {
                out.append(Line(raw: raw, key: nil, value: "")); continue
            }
            let key = String(raw[raw.startIndex..<eq]).trimmingCharacters(in: .whitespaces)
            var val = String(raw[raw.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
            if val.count >= 2, let f = val.first, (f == "\"" || f == "'"), val.last == f {
                val = String(val.dropFirst().dropLast())
            }
            out.append(Line(raw: raw, key: key, value: val))
        }
        // A trailing newline produces a final empty element from split; drop it so
        // serialize() round-trips without growing blank lines.
        if let last = out.last, last.key == nil, last.raw.isEmpty { out.removeLast() }
        lines = out
    }

    public func get(_ key: String) -> String {
        var last = ""
        for l in lines where l.key == key { last = l.value }
        return last
    }

    public mutating func set(_ key: String, _ value: String) {
        var lastIdx: Int? = nil
        for i in lines.indices where lines[i].key == key { lastIdx = i }
        if let i = lastIdx {
            lines[i].value = value
            lines[i].raw = "\(key)=\(value)"
        } else {
            lines.append(Line(raw: "\(key)=\(value)", key: key, value: value))
        }
    }

    public func serialize() -> String {
        lines.map { l in
            if let key = l.key { return "\(key)=\(l.value)" }
            return l.raw
        }.joined(separator: "\n") + "\n"
    }

    /// Atomic 0600 write via temp file + rename. Refuses if the dir is wider than
    /// 0700 (prevents same-machine non-owner reads of the token), matching envfile.go.
    public func write(to path: URL) throws {
        let dir = path.deletingLastPathComponent()
        let fm = FileManager.default
        if let attrs = try? fm.attributesOfItem(atPath: dir.path),
           let perm = (attrs[.posixPermissions] as? NSNumber)?.int16Value, (perm & 0o077) != 0 {
            throw ValidationError(message: "config dir \(dir.path) is wider than 0700; run chmod 0700 and retry")
        }
        try fm.createDirectory(at: dir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        let tmp = dir.appendingPathComponent(".tmp-\(UUID().uuidString).env")
        try serialize().write(to: tmp, atomically: false, encoding: .utf8)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp.path)
        // Atomic replace when the file exists; plain move on first write
        // (replaceItemAt fails if the destination doesn't exist yet).
        if fm.fileExists(atPath: path.path) {
            _ = try fm.replaceItemAt(path, withItemAt: tmp)
        } else {
            try fm.moveItem(at: tmp, to: path)
        }
        // Guarantee 0600 on the final path regardless of replace semantics.
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
    }
}

/// Default-true toggle parse (matches producer isEnvTrue): only false/0/no/off disable.
public func envTrue(_ v: String) -> Bool {
    switch v.trimmingCharacters(in: .whitespaces).lowercased() {
    case "false", "0", "no", "off": return false
    default: return true
    }
}

/// Default-false toggle parse (matches isEnvOn): only true/1/yes/on enable.
public func envOn(_ v: String) -> Bool {
    switch v.trimmingCharacters(in: .whitespaces).lowercased() {
    case "true", "1", "yes", "on": return true
    default: return false
    }
}
