import Foundation

public enum KnobEvent: Equatable, Sendable {
    case improv(ImprovCodec.Message)
    case cinder(CinderLineCodec.Message)
    case log(String)
}

public struct KnobStreamDemuxer: Sendable {
    static let maxBuffer = 4096
    private var buffer: [UInt8] = []

    public init() {}

    public mutating func feed<S: Sequence>(_ bytes: S) -> [KnobEvent] where S.Element == UInt8 {
        buffer.append(contentsOf: bytes)
        var out: [KnobEvent] = []
        while let event = next() { if let e = event { out.append(e) } }
        if buffer.count > Self.maxBuffer {
            buffer.removeFirst(buffer.count - Self.maxBuffer)
        }
        return out
    }

    private mutating func next() -> KnobEvent?? {
        let header = ImprovCodec.header
        let frameAt = Self.findFrame(header, in: buffer)
        let newlineAt = buffer.firstIndex(of: 0x0A)
        if let f = frameAt, newlineAt.map({ f <= $0 }) ?? true {
            if f > 0 {
                guard buffer.count >= f + header.count + 2 else { return nil }
                let text = Array(buffer[0..<f])
                buffer.removeFirst(f)
                return .some(Self.lineEvent(text))
            }
            let prefix = ImprovCodec.prefixLength
            guard buffer.count >= prefix else { return nil }
            let total = prefix + Int(buffer[prefix - 1]) + 1
            guard buffer.count >= total else { return nil }
            let bytes = Array(buffer[0..<total])
            if let msg = try? ImprovCodec.decode(bytes) {
                buffer.removeFirst(total)
                if buffer.first == 0x0A { buffer.removeFirst() }
                return .some(.improv(msg))
            }
            buffer.removeFirst()
            return .some(nil)
        }
        if let n = newlineAt {
            let text = Array(buffer[0..<n])
            buffer.removeFirst(n + 1)
            return .some(Self.lineEvent(text))
        }
        return nil
    }

    private static func lineEvent(_ bytes: [UInt8]) -> KnobEvent? {
        var line = String(decoding: bytes, as: UTF8.self)
        if line.hasSuffix("\r") { line.removeLast() }
        guard !line.trimmingCharacters(in: .whitespaces).isEmpty else { return nil }
        if line.hasPrefix(CinderLineCodec.prefix) {
            return CinderLineCodec.decode(line: line).map(KnobEvent.cinder) ?? .log(line)
        }
        return .log(line)
    }

    static func findFrame(_ needle: [UInt8], in hay: [UInt8]) -> Int? {
        guard hay.count >= needle.count else { return nil }
        for i in 0...(hay.count - needle.count) where hay[i] == needle[0] {
            guard Array(hay[i..<(i + needle.count)]) == needle else { continue }
            let v = i + needle.count
            if v < hay.count, hay[v] != ImprovCodec.version { continue }
            if v + 1 < hay.count, !(1...4).contains(hay[v + 1]) { continue }
            return i
        }
        return nil
    }
}
