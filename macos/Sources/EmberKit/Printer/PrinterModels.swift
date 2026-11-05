import Foundation

public struct PrinterConfig: Codable, Sendable, Equatable {
    public static let auto = "auto"
    public static let holdRange = 1...1440

    public var enabled: Bool
    public var url: String
    public var name: String
    public var clockApp: Bool
    public var notifyDone: Bool
    public var notifyAttention: Bool
    public var doneHoldMin: Int

    public init(enabled: Bool = false, url: String = PrinterConfig.auto, name: String = "CORE One",
                clockApp: Bool = true, notifyDone: Bool = true, notifyAttention: Bool = true,
                doneHoldMin: Int = 30) {
        self.enabled = enabled
        self.url = url
        self.name = name
        self.clockApp = clockApp
        self.notifyDone = notifyDone
        self.notifyAttention = notifyAttention
        self.doneHoldMin = doneHoldMin
    }

    enum CodingKeys: String, CodingKey {
        case enabled, url, name
        case clockApp = "clock_app"
        case notifyDone = "notify_done"
        case notifyAttention = "notify_attention"
        case doneHoldMin = "done_hold_min"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        let url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        self.url = url.isEmpty ? Self.auto : url
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? "CORE One"
        clockApp = try c.decodeIfPresent(Bool.self, forKey: .clockApp) ?? true
        notifyDone = try c.decodeIfPresent(Bool.self, forKey: .notifyDone) ?? true
        notifyAttention = try c.decodeIfPresent(Bool.self, forKey: .notifyAttention) ?? true
        doneHoldMin = try c.decodeIfPresent(Int.self, forKey: .doneHoldMin) ?? 30
    }

    public var isAuto: Bool { url == Self.auto }

    public var manualURL: String {
        get { isAuto ? "" : url }
        set { url = newValue.trimmingCharacters(in: .whitespaces) }
    }
}

public struct PrinterInfo: Codable, Sendable, Equatable {
    public var name: String
    public var host: String
    public var url: String
    public var discovered: Bool
    public var hostname: String
    public var firmware: String

    public init(name: String = "", host: String = "", url: String = "", discovered: Bool = false,
                hostname: String = "", firmware: String = "") {
        self.name = name
        self.host = host
        self.url = url
        self.discovered = discovered
        self.hostname = hostname
        self.firmware = firmware
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        host = try c.decodeIfPresent(String.self, forKey: .host) ?? ""
        url = try c.decodeIfPresent(String.self, forKey: .url) ?? ""
        discovered = try c.decodeIfPresent(Bool.self, forKey: .discovered) ?? false
        hostname = try c.decodeIfPresent(String.self, forKey: .hostname) ?? ""
        firmware = try c.decodeIfPresent(String.self, forKey: .firmware) ?? ""
    }

    enum CodingKeys: String, CodingKey { case name, host, url, discovered, hostname, firmware }
}

public struct PrinterState: Codable, Sendable, Equatable {
    public var enabled: Bool
    public var phase: String
    public var lastError: String
    public var printer: PrinterInfo

    public init(enabled: Bool = false, phase: String = "off", lastError: String = "",
                printer: PrinterInfo = PrinterInfo()) {
        self.enabled = enabled
        self.phase = phase
        self.lastError = lastError
        self.printer = printer
    }

    enum CodingKeys: String, CodingKey {
        case enabled, phase, printer
        case lastError = "last_error"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        phase = try c.decodeIfPresent(String.self, forKey: .phase) ?? "off"
        lastError = try c.decodeIfPresent(String.self, forKey: .lastError) ?? ""
        printer = try c.decodeIfPresent(PrinterInfo.self, forKey: .printer) ?? PrinterInfo()
    }
}

public enum PrinterStatusLine: Equatable, Sendable {
    case unknown
    case disabled
    case connected(firmware: String)
    case wrongPassword
    case passwordMissing
    case notFound
    case printerOff
    case other(String)

    public init(_ state: PrinterState?) {
        guard let state else { self = .unknown; return }
        guard state.enabled else { self = .disabled; return }
        switch state.lastError {
        case "": self = state.phase == "off" ? .passwordMissing : .connected(firmware: state.printer.firmware)
        case "wrong_password": self = .wrongPassword
        case "no_password": self = .passwordMissing
        case "not_found": self = .notFound
        case "unreachable": self = .printerOff
        default: self = .other(state.lastError)
        }
    }

    public var isProblem: Bool {
        switch self {
        case .wrongPassword, .passwordMissing, .notFound, .printerOff, .other: true
        case .unknown, .disabled, .connected: false
        }
    }

    public var title: LocalizedStringResource {
        switch self {
        case .unknown: "Checking…"
        case .disabled: "Off"
        case .connected(let firmware):
            firmware.isEmpty ? "Connected" : "Connected · firmware \(firmware)"
        case .wrongPassword: "Wrong password"
        case .passwordMissing: "Password missing on the server"
        case .notFound: "Not found on the network"
        case .printerOff: "Printer off"
        case .other(let code): "Problem: \(code)"
        }
    }

    public var detail: LocalizedStringResource? {
        switch self {
        case .wrongPassword:
            "The server's EMBER_PRUSALINK_PASSWORD isn't the printer's PrusaLink password. Read the right one on the printer under Settings → Network → PrusaLink, then fix it in the server's container."
        case .passwordMissing:
            "Set EMBER_PRUSALINK_PASSWORD in the Ember server's container. Read it on the printer under Settings → Network → PrusaLink. This app never asks for or stores it."
        case .notFound:
            "Ember found no PrusaLink printer on the network. Check the printer is on the same network, or enter its address."
        case .printerOff:
            "The printer didn't answer. It may be switched off or asleep."
        case .unknown, .disabled, .connected, .other: nil
        }
    }
}
