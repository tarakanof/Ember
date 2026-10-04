import Testing
import Foundation
import Darwin
@testable import EmberKit

/// A fake knob on the master side of a pty: the real `SerialPortLink` opens
/// the slave like a `/dev/cu.*` port. No real serial device is touched.
private final class PtyKnob: @unchecked Sendable {
    let master: Int32
    let slave: Int32
    let path: String
    private let queue = DispatchQueue(label: "pty-knob")
    private var stopped = false
    private let lock = NSLock()

    init() throws {
        var m: Int32 = -1, s: Int32 = -1
        guard openpty(&m, &s, nil, nil, nil) == 0 else { throw KnobLinkError.openFailed("openpty") }
        master = m
        slave = s
        path = String(cString: ttyname(s))
        var t = termios()
        tcgetattr(s, &t)
        cfmakeraw(&t)
        tcsetattr(s, TCSANOW, &t)
    }

    /// Answers device info and `CINDER1 info`, with log lines in between.
    func run() {
        queue.async { [self] in
            var host = KnobStreamDemuxer()
            var buf = [UInt8](repeating: 0, count: 512)
            while !(lock.withLock { stopped }) {
                var p = pollfd(fd: master, events: Int16(POLLIN), revents: 0)
                guard poll(&p, 1, 50) > 0 else { continue }
                let n = read(master, &buf, buf.count)
                guard n > 0 else { return }
                for e in host.feed(buf[0..<n]) {
                    switch e {
                    case .improv(.rpc(ImprovCodec.Command.deviceInfo.rawValue, _)):
                        write(Array("I (812) cinder: hello from the pty\r\nW (813) lvgl: noisy".utf8))
                        write(try! ImprovCodec.result(.deviceInfo, ["cinder", "0.5.0", "ESP32-S3", "Knob 61FC8C"]))
                        write(Array(" log tail\n".utf8))
                    case .log(let line) where line.hasPrefix("CINDER1 "):
                        write(Array("D (900) provision: got line\n".utf8))
                        write(Array(#"CINDER1 {"id":1,"ok":true,"fw":"0.5.0","hw_id":"3cdc7561fc8c","device_id":"knob-61fc8c","wifi":{"configured":true},"ember":{"configured":true}}"#.utf8) + [0x0A])
                    default:
                        break
                    }
                }
            }
        }
    }

    private func write(_ bytes: [UInt8]) {
        _ = bytes.withUnsafeBytes { Darwin.write(master, $0.baseAddress, $0.count) }
    }

    func stop() {
        lock.withLock { stopped = true }
        queue.sync {}
        close(master)
        close(slave)
    }
}

@Test func serialLinkTalksToAScriptedKnobOverAPty() async throws {
    let knob = try PtyKnob()
    defer { knob.stop() }
    knob.run()
    let p = KnobProvisioner(opener: SerialPortOpener(ports: { [] }),
                            mint: { _, _ in throw KnobSetupError.disconnected },
                            checkedIn: { _, _ in false })
    let (session, id) = try await p.connect(path: knob.path, usbHwID: nil)
    #expect(id.info == ImprovDeviceInfo(firmware: "cinder", version: "0.5.0", chip: "ESP32-S3", name: "Knob 61FC8C"))
    #expect(id.hwID == "3cdc7561fc8c")
    #expect(id.deviceID == "knob-61fc8c")
    #expect(id.isProvisioned)
    await session.close()
}

@Test func serialLinkConfiguresRawWithoutHangup() throws {
    let knob = try PtyKnob()
    defer { knob.stop() }
    let link = try SerialPortLink(path: knob.path)
    defer { link.close() }
    // Configure applied to the same tty: HUPCL cleared, so close leaves DTR/RTS alone.
    var t = termios()
    #expect(tcgetattr(knob.slave, &t) == 0)
    #expect(t.c_cflag & tcflag_t(HUPCL) == 0)
    #expect(t.c_lflag & tcflag_t(ICANON) == 0)
}

@Test func serialLinkOpenFailsForMissingPort() {
    #expect(throws: KnobLinkError.self) { _ = try SerialPortLink(path: "/dev/cu.ember-test-does-not-exist") }
}

@Test func knobPortFilterAndHwID() {
    let knob = KnobSerialPort(path: "/dev/cu.usbmodem1101", vendorID: 0x303A, productID: 0x1001, serialNumber: "3C:DC:75:61:FC:8C")
    #expect(knob.isKnobCandidate)
    #expect(knob.hwID == "3cdc7561fc8c")
    #expect(!KnobSerialPort(path: "/dev/tty.usbmodem1101", vendorID: 0x303A, productID: 0x1001, serialNumber: nil).isKnobCandidate)
    #expect(!KnobSerialPort(path: "/dev/cu.usbserial", vendorID: 0x10C4, productID: 0xEA60, serialNumber: "0001").isKnobCandidate)
    #expect(KnobSerialPort(path: "/dev/cu.x", vendorID: 0x303A, productID: 0x1001, serialNumber: "0001").hwID == nil)
}

@Test func serialLinkTakesThePortExclusively() throws {
    let knob = try PtyKnob()
    defer { knob.stop() }
    let first = try SerialPortLink(path: knob.path)
    #expect(throws: KnobLinkError.busy) { _ = try SerialPortLink(path: knob.path) }
    first.close()
    let again = try SerialPortLink(path: knob.path)
    again.close()
}

@Test func droppedSerialLinkClosesItsDescriptor() async throws {
    let knob = try PtyKnob()
    defer { knob.stop() }
    do {
        let link = try SerialPortLink(path: knob.path)
        _ = link.path
    }
    // The port's exclusive lock goes away only when the fd is closed.
    var reopened: SerialPortLink?
    for _ in 0..<100 where reopened == nil {
        reopened = try? SerialPortLink(path: knob.path)
        if reopened == nil { try await Task.sleep(for: .milliseconds(10)) }
    }
    #expect(reopened != nil)
    reopened?.close()
}
