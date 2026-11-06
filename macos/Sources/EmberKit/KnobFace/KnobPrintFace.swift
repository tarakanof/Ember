import Foundation

public struct KnobPrintBlock: Equatable, Sendable {
    public enum Phase: String, Sendable, CaseIterable {
        case unknown, offline, idle, heating, preparing, printing, finishing, paused, attention, finished, stopped, error
    }

    public struct Temps: Equatable, Sendable {
        public var nozzle, nozzleTarget, bed, bedTarget: Int

        public init(nozzle: Int, nozzleTarget: Int, bed: Int, bedTarget: Int) {
            self.nozzle = nozzle; self.nozzleTarget = nozzleTarget; self.bed = bed; self.bedTarget = bedTarget
        }
    }

    public struct Event: Equatable, Sendable {
        public var kind: String
        public var at: Int64

        public init(kind: String, at: Int64) {
            self.kind = kind; self.at = at
        }
    }

    static let printerRunes = 32
    static let materialRunes = 16
    static let tempMax = 500

    public var phase: Phase
    public var progress: Int?
    public var endsAt, startedAt, changeAt: Int64?
    public var temps: Temps?
    public var name, material, printer: String
    public var event: Event?

    public init(phase: Phase, progress: Int? = nil, endsAt: Int64? = nil, startedAt: Int64? = nil, changeAt: Int64? = nil,
                temps: Temps? = nil, name: String = "", material: String = "", printer: String = "", event: Event? = nil) {
        self.phase = phase; self.progress = progress; self.endsAt = endsAt; self.startedAt = startedAt
        self.changeAt = changeAt; self.temps = temps; self.name = name; self.material = material; self.printer = printer
        self.event = event
    }

    public init?(state: PrinterState?) {
        guard let s = state, s.enabled, s.phase != "off" else { return nil }
        let phase = Phase(rawValue: s.phase) ?? .unknown
        let event = s.event.map { Event(kind: $0.kind, at: $0.at) }
        let printer = String(s.printer.name.prefix(Self.printerRunes))
        guard phase != .offline, phase != .idle else {
            self.init(phase: phase, printer: printer, event: event)
            return
        }
        var temps: Temps?
        if phase == .heating, let n = s.nozzle, let nt = s.nozzleTarget, let b = s.bed, let bt = s.bedTarget,
           [n, nt, b, bt].allSatisfy({ (0...Self.tempMax).contains($0) }) {
            temps = Temps(nozzle: n, nozzleTarget: nt, bed: b, bedTarget: bt)
        }
        func time(_ t: Int64?) -> Int64? { t.flatMap { $0 > 0 ? $0 : nil } }
        self.init(phase: phase, progress: s.progress.flatMap { (0...100).contains($0) ? $0 : nil },
                  endsAt: time(s.endsAt), startedAt: time(s.startedAt), changeAt: time(s.changeAt), temps: temps,
                  name: s.name, material: String(s.material.prefix(Self.materialRunes)), printer: printer, event: event)
    }
}

public struct KnobPrintFace: Equatable, Sendable {
    public enum Look: Sendable, Equatable {
        case none, offline, idle, heating, progress, finishing, paused, attention, done, stopped, error
    }

    public var look: Look
    public var sweeps: Bool
    public var ring: Double
    public var ringColor: RGB
    public var head: Bool
    public var pulses: Bool
    public var heroProgress: Double
    public var heroHeat: Double
    public var value, unit, word, sub, small: String
    public var valueColor, unitColor, wordColor, subColor, smallColor: RGB
    public var dimmed: Bool

    public init(block b: KnobPrintBlock?, now: Date, zone: TimeZone? = .current,
                theme: KnobTheme.Print = KnobTheme.standard.print) {
        let c = theme.colors
        let look = Self.look(b)
        self.look = look
        sweeps = b?.phase == .printing
        ring = Self.ringFraction(b, look: look)
        (ringColor, head, pulses) = Self.ringLook(look, c)
        heroProgress = look == .heating ? 0 : ring
        heroHeat = look == .heating ? ring : 0
        value = ""; unit = ""; word = ""; sub = ""; small = ""
        valueColor = c.text; unitColor = c.sub; wordColor = c.text; subColor = c.sub; smallColor = c.small
        dimmed = false
        text(b, look: look, now: now.timeIntervalSince1970, zone: zone, theme: theme)
        if look == .done, let b {
            if let e = b.event, e.kind == "finished" {
                dimmed = now.timeIntervalSince1970 - Double(e.at) >= theme.doneDimS
            } else {
                dimmed = true
            }
        }
    }

    static func look(_ b: KnobPrintBlock?) -> Look {
        guard let b else { return .none }
        switch b.phase {
        case .offline: return .offline
        case .unknown, .idle: return .idle
        case .heating: return .heating
        case .preparing, .printing: return .progress
        case .finishing: return .finishing
        case .paused: return .paused
        case .attention: return .attention
        case .finished: return .done
        case .stopped: return .stopped
        case .error: return .error
        }
    }

    static func clamp01(_ v: Double) -> Double { min(max(v, 0), 1) }

    static func heat(_ t: KnobPrintBlock.Temps?) -> Double {
        guard let t else { return 0 }
        let n = t.nozzleTarget > 25 ? clamp01((Double(t.nozzle) - 25) / (Double(t.nozzleTarget) - 25)) : 0
        let d = t.bedTarget > 22 ? clamp01((Double(t.bed) - 22) / (Double(t.bedTarget) - 22)) : 0
        return n * 0.6 + d * 0.4
    }

    static func ringFraction(_ b: KnobPrintBlock?, look: Look) -> Double {
        switch look {
        case .none, .offline, .idle: 0
        case .heating: heat(b?.temps)
        case .finishing, .done: 1
        default: Double(b?.progress ?? 0) / 100
        }
    }

    static func ringLook(_ l: Look, _ c: KnobTheme.Print.Colors) -> (RGB, Bool, Bool) {
        switch l {
        case .heating: (c.heat, true, false)
        case .paused: (mix(.init(r: 0, g: 0, b: 0), c.accent, 0.5), false, false)
        case .attention: (c.wait, true, true)
        case .done: (c.done, false, false)
        case .stopped: (c.grey, false, false)
        case .error: (c.err, false, true)
        case .none, .offline, .idle: (c.track, false, false)
        case .progress, .finishing: (c.accent, true, false)
        }
    }

    public static func mix(_ a: RGB, _ b: RGB, _ k: Double) -> RGB {
        func m(_ x: UInt8, _ y: UInt8) -> UInt8 {
            UInt8(min(max((Double(x) + (Double(y) - Double(x)) * k).rounded(), 0), 255))
        }
        return RGB(r: m(a.r, b.r), g: m(a.g, b.g), b: m(a.b, b.b))
    }

    static let sep = " \u{2022} "

    static func leftSeconds(_ b: KnobPrintBlock, now: Double) -> Int64? {
        guard let end = b.endsAt, now > 0 else { return nil }
        let left = Double(end) - now
        return left > 0 ? Int64(left.rounded(.up)) : 0
    }

    static func leftMinutes(_ b: KnobPrintBlock, now: Double) -> Int? {
        leftSeconds(b, now: now).map { Int(($0 + 59) / 60) }
    }

    static func leftText(_ min: Int) -> (value: String, unit: String) {
        let m = max(min, 0)
        return m >= 60 ? (String(format: "%d:%02d", m / 60, m % 60), "h left") : ("\(m)", "min left")
    }

    static func hhmm(_ t: Int64, offset: Int) -> String {
        var d = (t + Int64(offset)) % 86400
        if d < 0 { d += 86400 }
        return String(format: "%02d:%02d", Int(d / 3600), Int(d / 60 % 60))
    }

    static func duration(_ s: Int64) -> String {
        let m = s < 0 ? 0 : (s + 30) / 60
        return m >= 60 ? String(format: "%lld h %02lld m", m / 60, m % 60) : "\(m) min"
    }

    static func took(_ b: KnobPrintBlock) -> Int64? {
        guard let e = b.event, e.kind == "finished", let start = b.startedAt, e.at > start else { return nil }
        return e.at - start
    }

    static func changeSoon(_ b: KnobPrintBlock, now: Double, within: Double) -> Int? {
        guard let at = b.changeAt, now > 0 else { return nil }
        let left = Double(at) - now
        guard left >= 0, left <= within else { return nil }
        return Int((left / 60).rounded(.up))
    }

    static func fold(_ s: String) -> String { KnobTextFold.fold(s) }

    private mutating func setWord(_ w: String, _ c: RGB) { word = w; wordColor = c }
    private mutating func setSub(_ s: String, _ c: RGB) { sub = s; subColor = c }

    private mutating func text(_ b: KnobPrintBlock?, look: Look, now: Double, zone: TimeZone?, theme: KnobTheme.Print) {
        let c = theme.colors
        let p = b?.progress ?? 0
        let printer = Self.fold(b?.printer ?? "")
        if let b, look == .heating || look == .progress {
            let name = Self.fold(b.name), material = Self.fold(b.material)
            small = !name.isEmpty && !material.isEmpty ? name + Self.sep + material : name.isEmpty ? material : name
        }
        switch look {
        case .none:
            setWord("Printer off", c.dimWord)
        case .offline:
            setWord("Printer off", c.dimWord)
            setSub(printer, c.dimWord)
        case .idle:
            guard b?.phase == .idle else {
                setWord("Idle", c.sub)
                setSub(printer, c.small)
                break
            }
            setWord("Ready", c.text)
            if let t = b?.temps, t.bed > theme.warmC {
                setSub("bed \(t.bed)\u{B0}", c.heat)
            } else if let t = b?.temps, t.nozzle > theme.warmC {
                setSub("nozzle \(t.nozzle)\u{B0}", c.heat)
            } else {
                setSub(printer, c.small)
            }
        case .heating:
            guard let t = b?.temps else {
                setWord("Heating", c.heat)
                break
            }
            value = "\(t.nozzle)\u{B0}"
            unit = "/ \(t.nozzleTarget)\u{B0}"
            valueColor = Self.mix(c.text, c.heat, 0.5)
            unitColor = c.sub
            setSub("heating" + Self.sep + "bed \(t.bed)\u{B0}", c.heat)
        case .progress:
            guard let b else { break }
            progressText(b, now: now, zone: zone, theme: theme)
        case .finishing:
            value = "100"
            unit = "%"
            valueColor = c.text
            unitColor = c.sub
            setSub("finishing up", c.accent)
        case .paused:
            setWord("Paused", c.wait)
            setSub("at \(p)%", c.sub)
        case .attention:
            setWord("Check printer", c.wait)
            setSub(p > 0 ? "paused" + Self.sep + "\(p)% done" : "see the printer", c.sub)
        case .done:
            setWord("Done", c.done)
            if let b, let took = Self.took(b) {
                setSub(Self.duration(took) + Self.sep + "take it out", c.sub)
            } else {
                setSub("take it out", c.sub)
            }
        case .stopped:
            setWord("Stopped", c.sub)
            setSub("at \(p)%", c.sub)
        case .error:
            setWord("Printer error", c.err)
            setSub("see the printer", c.sub)
        }
    }

    private mutating func progressText(_ b: KnobPrintBlock, now: Double, zone: TimeZone?, theme: KnobTheme.Print) {
        let c = theme.colors
        guard let min = Self.leftMinutes(b, now: now), let end = b.endsAt else {
            setWord(b.phase == .preparing ? "Preparing" : "Printing", c.accent)
            if let p = b.progress, b.phase == .printing { setSub("\(p)% done", c.sub) }
            return
        }
        (value, unit) = Self.leftText(min)
        valueColor = c.text
        unitColor = c.sub
        if let ch = Self.changeSoon(b, now: now, within: theme.changeSoonS) {
            setSub("colour change: \(ch) min", c.wait)
        } else if let zone {
            let offset = zone.secondsFromGMT(for: Date(timeIntervalSince1970: TimeInterval(end)))
            setSub("done " + Self.hhmm(end, offset: offset), c.accent)
        } else {
            setSub("done in " + Self.duration(Int64(min) * 60), c.accent)
        }
    }

    public static func fit(_ s: String, width: Double, measure: (String) -> Double) -> String {
        guard !s.isEmpty, measure(s) > width else { return s }
        let chars = Array(s)
        var tail = ""
        var head = chars.count
        if let r = s.range(of: sep, options: .backwards) {
            tail = String(s[r.lowerBound...])
            head = s.distance(from: s.startIndex, to: r.lowerBound)
        }
        for _ in 0..<2 {
            var n = head
            while n > 0 {
                n -= 1
                var k = n
                while k > 0, chars[k - 1] == " " { k -= 1 }
                let out = String(chars[..<k]) + "..." + tail
                if measure(out) <= width { return out }
            }
            tail = ""
            head = chars.count
        }
        return ""
    }

    public static func headX(sweeps: Bool, look: Look, t: Double?) -> Double {
        if sweeps { return sin((t ?? 0) * 2.6) * 20 }
        return look == .heating ? 0 : 30
    }

    public static func pulse(_ look: Look, t: Double?) -> Double {
        guard look == .attention || look == .error, let t else { return 1 }
        return 0.35 + 0.65 * (0.5 + 0.5 * sin(t * 5))
    }

    public var moves: Bool { sweeps || pulses }
}
