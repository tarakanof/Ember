import Foundation

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

    public mutating func remove(_ key: String) {
        lines.removeAll { $0.key == key }
    }

    public func serialize() -> String {
        lines.map { l in
            if let key = l.key { return "\(key)=\(l.value)" }
            return l.raw
        }.joined(separator: "\n") + "\n"
    }

    public func write(to path: URL) throws {
        let dir = path.deletingLastPathComponent()
        let fm = FileManager.default
        if let attrs = try? fm.attributesOfItem(atPath: dir.path),
           let perm = (attrs[.posixPermissions] as? NSNumber)?.int16Value, (perm & 0o077) != 0 {
            throw ValidationError(message: LocalizedStringResource(
                "Other users can read \(dir.path). Run chmod 700 on that folder and try again.",
                comment: "Saving settings failed: the folder holding the token is readable by other users. The argument is its path."))
        }
        try fm.createDirectory(at: dir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        let tmp = dir.appendingPathComponent(".tmp-\(UUID().uuidString).env")
        try serialize().write(to: tmp, atomically: false, encoding: .utf8)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp.path)
        if fm.fileExists(atPath: path.path) {
            _ = try fm.replaceItemAt(path, withItemAt: tmp)
        } else {
            try fm.moveItem(at: tmp, to: path)
        }
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
    }
}

public func envTrue(_ v: String) -> Bool {
    switch v.trimmingCharacters(in: .whitespaces).lowercased() {
    case "false", "0", "no", "off": return false
    default: return true
    }
}

public func envOn(_ v: String) -> Bool {
    switch v.trimmingCharacters(in: .whitespaces).lowercased() {
    case "true", "1", "yes", "on": return true
    default: return false
    }
}
