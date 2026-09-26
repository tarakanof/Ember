import Foundation

/// A JSON value that can carry an explicit `null`, for request bodies the
/// synthesized `Encodable` can't express (a settings patch that resets a
/// per-app colour to "inherit").
public enum JSONValue: Equatable, Sendable, Encodable {
    case null
    case bool(Bool)
    case int(Int)
    case double(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    /// Converts a `JSONSerialization` result. Unknown types become `null`.
    public init(_ any: Any) {
        switch any {
        case is NSNull:
            self = .null
        case let n as NSNumber:
            if CFGetTypeID(n) == CFBooleanGetTypeID() {
                self = .bool(n.boolValue)
            } else if CFNumberIsFloatType(n) {
                self = .double(n.doubleValue)
            } else {
                self = .int(n.intValue)
            }
        case let s as String:
            self = .string(s)
        case let a as [Any]:
            self = .array(a.map(JSONValue.init))
        case let o as [String: Any]:
            self = .object(o.mapValues(JSONValue.init))
        default:
            self = .null
        }
    }

    /// The object form of any `Encodable` value; nil when it isn't an object.
    public static func object<T: Encodable>(encoding value: T) throws -> [String: JSONValue]? {
        let data = try JSONEncoder().encode(value)
        guard case .object(let o) = JSONValue(try JSONSerialization.jsonObject(with: data)) else { return nil }
        return o
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let v): try c.encode(v)
        case .int(let v): try c.encode(v)
        case .double(let v): try c.encode(v)
        case .string(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .object(let v): try c.encode(v)
        }
    }
}
