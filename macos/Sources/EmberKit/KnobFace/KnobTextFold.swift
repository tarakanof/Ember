import Foundation

enum KnobTextFold {
    static let latin: [String] = [
        "A", "A", "A", "A", "A", "A", "AE", "C", "E", "E", "E", "E", "I", "I", "I", "I",
        "D", "N", "O", "O", "O", "O", "O", "x", "O", "U", "U", "U", "U", "Y", "Th", "ss",
        "a", "a", "a", "a", "a", "a", "ae", "c", "e", "e", "e", "e", "i", "i", "i", "i",
        "d", "n", "o", "o", "o", "o", "o", "/", "o", "u", "u", "u", "u", "y", "th", "y",
        "A", "a", "A", "a", "A", "a", "C", "c", "C", "c", "C", "c", "C", "c", "D", "d",
        "D", "d", "E", "e", "E", "e", "E", "e", "E", "e", "E", "e", "G", "g", "G", "g",
        "G", "g", "G", "g", "H", "h", "H", "h", "I", "i", "I", "i", "I", "i", "I", "i",
        "I", "i", "IJ", "ij", "J", "j", "K", "k", "k", "L", "l", "L", "l", "L", "l", "L",
        "l", "L", "l", "N", "n", "N", "n", "N", "n", "n", "N", "n", "O", "o", "O", "o",
        "O", "o", "OE", "oe", "R", "r", "R", "r", "R", "r", "S", "s", "S", "s", "S", "s",
        "S", "s", "T", "t", "T", "t", "T", "t", "U", "u", "U", "u", "U", "u", "U", "u",
        "U", "u", "U", "u", "W", "w", "Y", "y", "Y", "Z", "z", "Z", "z", "Z", "z", "s",
    ]

    static let cyrUpper: [String] = [
        "A", "B", "V", "G", "D", "E", "Zh", "Z", "I", "Y", "K", "L", "M", "N", "O", "P",
        "R", "S", "T", "U", "F", "Kh", "Ts", "Ch", "Sh", "Shch", "", "Y", "", "E", "Yu", "Ya",
    ]

    static let cyrLower: [String] = [
        "a", "b", "v", "g", "d", "e", "zh", "z", "i", "y", "k", "l", "m", "n", "o", "p",
        "r", "s", "t", "u", "f", "kh", "ts", "ch", "sh", "shch", "", "y", "", "e", "yu", "ya",
    ]

    static func fold(_ text: String) -> String {
        var out = ""
        for u in text.unicodeScalars {
            let cp = u.value
            if cp < 0x80 {
                out.unicodeScalars.append(cp < 0x20 || cp == 0x7F ? " " : u)
            } else {
                out += fold(codePoint: cp)
            }
        }
        return out
    }

    static func fold(codePoint cp: UInt32) -> String {
        if (0xC0...0x17F).contains(cp) { return latin[Int(cp) - 0xC0] }
        if (0x410...0x42F).contains(cp) { return cyrUpper[Int(cp) - 0x410] }
        if (0x430...0x44F).contains(cp) { return cyrLower[Int(cp) - 0x430] }
        switch cp {
        case 0x401: return "Yo"
        case 0x451: return "yo"
        case 0x404: return "Ye"
        case 0x454: return "ye"
        case 0x406: return "I"
        case 0x456: return "i"
        case 0x407: return "Yi"
        case 0x457: return "yi"
        case 0x490: return "G"
        case 0x491: return "g"
        case 0x40E: return "U"
        case 0x45E: return "u"
        case 0xA0: return " "
        case 0xA1: return "!"
        case 0xBF: return "?"
        case 0xAB, 0xBB: return "\""
        case 0xB7, 0x2022, 0x2027: return "."
        case 0x2010...0x2015, 0x2212: return "-"
        case 0x2018, 0x2019, 0x201A, 0x201B, 0x2032: return "'"
        case 0x201C, 0x201D, 0x201E, 0x201F, 0x2033: return "\""
        case 0x2026: return "..."
        case 0x200B, 0x200C, 0x200D, 0xFEFF: return ""
        default: break
        }
        if (0x2000...0x200A).contains(cp) { return " " }
        if (0x300...0x36F).contains(cp) { return "" }
        return "?"
    }
}
