import Darwin
import Foundation

/// A knob over a `/dev/cu.*` tty: raw termios, read with a dispatch source.
///
/// The ESP32-S3's USB-Serial/JTAG resets the chip on some DTR/RTS
/// transitions (that is how esptool resets it; toggling RTS resets the knob).
/// So the link never sets the modem lines (no `TIOCMBIS`/`TIOCMBIC`/
/// `TIOCMSET`) and clears `HUPCL`, so closing doesn't drop them either.
public final class SerialPortLink: KnobLink, @unchecked Sendable {
    public let path: String
    public let events: AsyncStream<KnobEvent>
    private let continuation: AsyncStream<KnobEvent>.Continuation
    private let fd: Int32
    private let queue: DispatchQueue
    private let source: DispatchSourceRead
    private let lock = NSLock()
    private var demuxer = KnobStreamDemuxer()
    private var isClosed = false

    public init(path: String) throws {
        let fd = Darwin.open(path, O_RDWR | O_NOCTTY | O_NONBLOCK)
        guard fd >= 0 else {
            throw errno == EBUSY ? KnobLinkError.busy : KnobLinkError.openFailed(String(cString: strerror(errno)))
        }
        // Exclusive, so Ember never splits the byte stream with idf.py
        // monitor or esptool (pyserial's exclusive=True takes the same flock).
        guard ioctl(fd, TIOCEXCL) == 0, flock(fd, LOCK_EX | LOCK_NB) == 0 else {
            Self.clearHangup(fd)
            Darwin.close(fd)
            throw KnobLinkError.busy
        }
        do {
            try Self.configure(fd)
        } catch {
            Darwin.close(fd)
            throw error
        }
        self.path = path
        self.fd = fd
        (events, continuation) = AsyncStream.makeStream(of: KnobEvent.self, bufferingPolicy: .bufferingNewest(256))
        queue = DispatchQueue(label: "ember.knob.serial")
        source = DispatchSource.makeReadSource(fileDescriptor: fd, queue: queue)
        source.setEventHandler { [weak self] in self?.readAvailable() }
        source.setCancelHandler { Darwin.close(fd) }
        continuation.onTermination = { [weak self] _ in self?.close() }
        source.resume()
    }

    /// Raw 8N1, no flow control, no hang-up on close. The baud rate is
    /// ignored by USB-Serial/JTAG but set for real UART bridges.
    static func configure(_ fd: Int32) throws {
        // HUPCL goes first, so a failure below can't drop DTR/RTS on close.
        guard clearHangup(fd) else { throw KnobLinkError.openFailed(String(cString: strerror(errno))) }
        var t = termios()
        guard tcgetattr(fd, &t) == 0 else { throw KnobLinkError.openFailed(String(cString: strerror(errno))) }
        cfmakeraw(&t)
        t.c_cflag |= tcflag_t(CLOCAL | CREAD)
        t.c_cflag &= ~tcflag_t(HUPCL | CRTSCTS)
        cfsetspeed(&t, speed_t(B115200))
        guard tcsetattr(fd, TCSANOW, &t) == 0 else { throw KnobLinkError.openFailed(String(cString: strerror(errno))) }
    }

    @discardableResult
    static func clearHangup(_ fd: Int32) -> Bool {
        var t = termios()
        guard tcgetattr(fd, &t) == 0 else { return false }
        if t.c_cflag & tcflag_t(HUPCL) == 0 { return true }
        t.c_cflag &= ~tcflag_t(HUPCL)
        return tcsetattr(fd, TCSANOW, &t) == 0
    }

    deinit { close() }

    private func readAvailable() {
        var buf = [UInt8](repeating: 0, count: 1024)
        while true {
            let n = buf.withUnsafeMutableBytes { Darwin.read(fd, $0.baseAddress, $0.count) }
            if n > 0 {
                let out = lock.withLock { demuxer.feed(buf[0..<n]) }
                for e in out { continuation.yield(e) }
                continue
            }
            if n < 0, errno == EAGAIN || errno == EINTR { return }
            // 0 = EOF, or an error such as ENXIO when the knob unplugs.
            close()
            return
        }
    }

    public func send(_ bytes: [UInt8]) async throws {
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
            queue.async { [self] in
                do { try writeAll(bytes); c.resume() } catch { c.resume(throwing: error) }
            }
        }
    }

    private func writeAll(_ bytes: [UInt8]) throws {
        guard !(lock.withLock { isClosed }) else { throw KnobLinkError.closed }
        var offset = 0
        var stalls = 0
        while offset < bytes.count {
            let n = bytes[offset...].withUnsafeBytes { Darwin.write(fd, $0.baseAddress, $0.count) }
            if n > 0 { offset += n; stalls = 0; continue }
            if n < 0, errno == EAGAIN || errno == EINTR {
                stalls += 1
                // Nobody reading on the knob side: give up after ~1 s.
                guard stalls < 100 else { throw KnobLinkError.writeFailed("timed out") }
                var p = pollfd(fd: fd, events: Int16(POLLOUT), revents: 0)
                _ = poll(&p, 1, 10)
                continue
            }
            throw KnobLinkError.writeFailed(String(cString: strerror(errno)))
        }
    }

    public func close() {
        let first = lock.withLock { () -> Bool in
            defer { isClosed = true }
            return !isClosed
        }
        guard first else { return }
        // Release the port now, not when the cancel handler closes the fd on
        // its queue: TIOCEXCL lives on the tty and outlasts this fd while
        // anything else holds it open, and the next opener mustn't race us.
        _ = ioctl(fd, TIOCNXCL)
        _ = flock(fd, LOCK_UN)
        source.cancel()
        continuation.finish()
    }
}

/// Opens `SerialPortLink`s, finding the knob again by USB serial number.
public struct SerialPortOpener: KnobLinkOpener {
    let ports: @Sendable () -> [KnobSerialPort]

    public init(ports: @escaping @Sendable () -> [KnobSerialPort] = { KnobSerialPorts.scan() }) {
        self.ports = ports
    }

    public func open(path: String) async throws -> any KnobLink {
        try SerialPortLink(path: path)
    }

    public func reopen(serialNumber: String, timeout: Duration) async throws -> any KnobLink {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeout)
        while clock.now < deadline {
            if let port = ports().first(where: { $0.serialNumber == serialNumber }),
               let link = try? SerialPortLink(path: port.path) {
                return link
            }
            try await Task.sleep(for: .milliseconds(250))
        }
        throw KnobLinkError.notFound
    }
}
