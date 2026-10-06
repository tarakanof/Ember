import Foundation
import IOKit
import IOKit.serial
import Observation

public struct KnobSerialPort: Equatable, Hashable, Sendable, Identifiable {
    public let path: String
    public let vendorID: Int
    public let productID: Int
    public let serialNumber: String?

    public var id: String { path }

    public init(path: String, vendorID: Int, productID: Int, serialNumber: String?) {
        self.path = path; self.vendorID = vendorID; self.productID = productID; self.serialNumber = serialNumber
    }

    public static let espressifVendorID = 0x303A
    public static let usbSerialJTAGProductID = 0x1001

    public var isKnobCandidate: Bool {
        vendorID == Self.espressifVendorID && productID == Self.usbSerialJTAGProductID
            && path.hasPrefix("/dev/cu.")
    }

    public var hwID: String? {
        guard let s = serialNumber else { return nil }
        let hex = s.lowercased().filter { $0 != ":" && $0 != "-" }
        return hex.count == 12 && hex.allSatisfy(\.isHexDigit) ? hex : nil
    }
}

@MainActor
@Observable
public final class KnobSerialPorts {
    public private(set) var ports: [KnobSerialPort] = []

    @ObservationIgnored private var notifyPort: IONotificationPortRef?
    @ObservationIgnored private var iterators: [io_iterator_t] = []
    @ObservationIgnored private let scanner: @Sendable () -> [KnobSerialPort]

    public init(scanner: @escaping @Sendable () -> [KnobSerialPort] = { KnobSerialPorts.scan() }) {
        self.scanner = scanner
    }

    public func start() {
        guard notifyPort == nil else { return }
        guard let port = IONotificationPortCreate(kIOMainPortDefault) else { rescan(); return }
        notifyPort = port
        IONotificationPortSetDispatchQueue(port, .main)
        let ctx = Unmanaged.passUnretained(self).toOpaque()
        let callback: IOServiceMatchingCallback = { refcon, iterator in
            KnobSerialPorts.drain(iterator)
            guard let refcon else { return }
            let me = Unmanaged<KnobSerialPorts>.fromOpaque(refcon).takeUnretainedValue()
            MainActor.assumeIsolated { me.rescan() }
        }
        for kind in [kIOFirstMatchNotification, kIOTerminatedNotification] {
            var it: io_iterator_t = 0
            let match = IOServiceMatching(kIOSerialBSDServiceValue)
            if IOServiceAddMatchingNotification(port, kind, match, callback, ctx, &it) == KERN_SUCCESS {
                Self.drain(it)
                iterators.append(it)
            }
        }
        rescan()
    }

    public func stop() {
        for it in iterators { IOObjectRelease(it) }
        iterators = []
        if let notifyPort { IONotificationPortDestroy(notifyPort) }
        notifyPort = nil
    }

    public func rescan() {
        let next = scanner()
        if next != ports { ports = next }
    }

    nonisolated private static func drain(_ it: io_iterator_t) {
        while case let obj = IOIteratorNext(it), obj != 0 { IOObjectRelease(obj) }
    }

    nonisolated public static func scan() -> [KnobSerialPort] {
        var it: io_iterator_t = 0
        guard IOServiceGetMatchingServices(kIOMainPortDefault, IOServiceMatching(kIOSerialBSDServiceValue), &it)
                == KERN_SUCCESS else { return [] }
        defer { IOObjectRelease(it) }
        var out: [KnobSerialPort] = []
        while case let service = IOIteratorNext(it), service != 0 {
            defer { IOObjectRelease(service) }
            guard let path = property(service, kIOCalloutDeviceKey, recursive: false) as? String else { continue }
            let vendor = (property(service, "idVendor", recursive: true) as? NSNumber)?.intValue ?? 0
            let product = (property(service, "idProduct", recursive: true) as? NSNumber)?.intValue ?? 0
            let serial = (property(service, "USB Serial Number", recursive: true)
                          ?? property(service, "kUSBSerialNumberString", recursive: true)) as? String
            let port = KnobSerialPort(path: path, vendorID: vendor, productID: product, serialNumber: serial)
            if port.isKnobCandidate { out.append(port) }
        }
        return out.sorted { $0.path < $1.path }
    }

    nonisolated private static func property(_ service: io_object_t, _ key: String, recursive: Bool) -> Any? {
        if recursive {
            return IORegistryEntrySearchCFProperty(service, kIOServicePlane, key as CFString, kCFAllocatorDefault,
                                                   IOOptionBits(kIORegistryIterateRecursively | kIORegistryIterateParents))
        }
        return IORegistryEntryCreateCFProperty(service, key as CFString, kCFAllocatorDefault, 0)?.takeRetainedValue()
    }
}
