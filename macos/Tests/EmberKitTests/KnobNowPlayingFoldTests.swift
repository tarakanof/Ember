import Foundation
import Testing
@testable import EmberKit

private func fold(_ s: String) -> String { KnobNowPlayingFace.fold(s) }

@Test func foldMatchesTheFirmwareOnRussianAndPunctuation() {
    #expect(fold("Мумий Тролль") == "Mumiy Troll")
    #expect(fold("Жуки — Щука «Чай»") == "Zhuki - Shchuka \"Chay\"")
    #expect(fold("Юля Цой Хор Объект") == "Yulya Tsoy Khor Obekt")
    #expect(fold("Щёлк") == "Shchyolk")
    #expect(fold("Cafe\u{301}") == "Cafe")
    #expect(fold("a\u{200B}b\u{2010}c×d") == "ab-cxd")
    #expect(fold("20°C • live") == "20?C . live")
    #expect(fold("Việt") == "Vi?t")
    #expect(fold("Hey Jude") == "Hey Jude")
    #expect(fold("Smile 😀") == "Smile ?")
    #expect(fold("東京") == "??")
    #expect(fold("tab\there") == "tab here")
}

private let cinderNP: URL? = {
    var candidates: [URL] = []
    if let env = ProcessInfo.processInfo.environment["CINDER_DIR"] { candidates.append(URL(fileURLWithPath: env)) }
    var root = URL(fileURLWithPath: #filePath)
    for _ in 0..<4 { root.deleteLastPathComponent() }
    candidates.append(root.deletingLastPathComponent().appendingPathComponent("cinder"))
    return candidates.first { FileManager.default.fileExists(atPath: $0.appendingPathComponent("firmware/components/nowplaying/np.c").path) }
}()

private func source(_ rel: String) throws -> String {
    try String(contentsOf: #require(cinderNP).appendingPathComponent("firmware/" + rel), encoding: .utf8)
}

private func table(_ name: String, in src: String) throws -> [String] {
    let re = try NSRegularExpression(pattern: "static const char \\*const " + name + "\\[\\d+\\] = \\{(.*?)\\};",
                                     options: [.dotMatchesLineSeparators])
    let m = try #require(re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)))
    let body = String(src[Range(m.range(at: 1), in: src)!])
    let lit = try NSRegularExpression(pattern: "\"([^\"]*)\"")
    return lit.matches(in: body, range: NSRange(body.startIndex..., in: body)).map { String(body[Range($0.range(at: 1), in: body)!]) }
}

@Test(.enabled(if: cinderNP != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func foldTablesMatchNpC() throws {
    let src = try source("components/nowplaying/np.c")
    #expect(try table("LATIN", in: src) == KnobTextFold.latin)
    #expect(try table("CYR_UPPER", in: src) == KnobTextFold.cyrUpper)
    #expect(try table("CYR_LOWER", in: src) == KnobTextFold.cyrLower)
    let re = try NSRegularExpression(pattern: "((?:case 0x[0-9A-Fa-f]+: ?)+)return \"((?:[^\"\\\\]|\\\\.)*)\";")
    let fn = try #require(src.range(of: "static const char *fold_cp"))
    let tail = String(src[fn.lowerBound...])
    let cases = re.matches(in: tail, range: NSRange(tail.startIndex..., in: tail))
    #expect(cases.count >= 20)
    for m in cases {
        let labels = String(tail[Range(m.range(at: 1), in: tail)!])
        let want = String(tail[Range(m.range(at: 2), in: tail)!]).replacingOccurrences(of: "\\\"", with: "\"")
        for h in labels.components(separatedBy: "case 0x").dropFirst() {
            let cp = try #require(UInt32(h.prefix { $0.isHexDigit }, radix: 16))
            #expect(KnobTextFold.fold(codePoint: cp) == want, "U+\(String(cp, radix: 16))")
        }
    }
    #expect(KnobTextFold.fold(codePoint: 0x2005) == " " && KnobTextFold.fold(codePoint: 0x2012) == "-")
    #expect(KnobTextFold.fold(codePoint: 0x301) == "" && KnobTextFold.fold(codePoint: 0x1F600) == "?")
}

private func cLiterals(_ s: Substring) -> [[UInt8]] {
    var out: [[UInt8]] = [], cur: [UInt8]? = nil
    let b = Array(s.utf8)
    var i = 0
    while i < b.count {
        if b[i] == UInt8(ascii: "\"") {
            var bytes = cur ?? []
            i += 1
            while i < b.count, b[i] != UInt8(ascii: "\"") {
                if b[i] == UInt8(ascii: "\\"), i + 1 < b.count {
                    i += 1
                    if b[i] == UInt8(ascii: "x") {
                        var v = 0, n = 0
                        while i + 1 < b.count, n < 2, let d = Character(UnicodeScalar(b[i + 1])).hexDigitValue { v = v * 16 + d; i += 1; n += 1 }
                        bytes.append(UInt8(v))
                    } else {
                        bytes.append(b[i] == UInt8(ascii: "n") ? 10 : b[i] == UInt8(ascii: "t") ? 9 : b[i] == UInt8(ascii: "r") ? 13 : b[i])
                    }
                } else {
                    bytes.append(b[i])
                }
                i += 1
            }
            cur = bytes
        } else if b[i] == UInt8(ascii: ","), let c = cur {
            out.append(c); cur = nil
        }
        i += 1
    }
    if let c = cur { out.append(c) }
    return out
}

@Test(.enabled(if: cinderNP != nil, "no cinder checkout next to this repo (set CINDER_DIR)"))
func foldAgreesWithTheFirmwareHostTestVectors() throws {
    let src = try source("test/host/test_np.c")
    var checked = 0
    var rest = src[...]
    while let r = rest.range(of: "fold_is(") {
        rest = rest[r.upperBound...]
        guard let end = rest.range(of: ");") else { break }
        let lits = cLiterals(rest[..<end.lowerBound])
        guard lits.count == 2, let input = String(bytes: lits[0], encoding: .utf8), let want = String(bytes: lits[1], encoding: .utf8)
        else { continue }
        #expect(fold(input) == want, "fold \(input)")
        checked += 1
    }
    #expect(checked >= 10)
}
