import Charts
import SwiftUI
import EmberKit

/// What the knob's Hardware page reads: plain values and callbacks, so
/// previews and snapshot renders use `KnobStatsFake`.
struct KnobHardwareInput {
    var knobID: String
    var firmware: String?
    var ipAddress: String?
    var stats: Loadable<KnobStats>
    var range: HardwareRange
    var setRange: @MainActor @Sendable (HardwareRange) -> Void = { _ in }
    var setDiagnostics: @MainActor @Sendable (KnobDiagnostics) -> Void = { _ in }
    var savingDiagnostics = false
    var diagnosticsError: String?
}

enum KnobCardID: Hashable, Sendable {
    case overview, cpu, memory, psram, temperature, wifi, requests, latency, rendering
}

/// Settings › Devices › Knob › Hardware: the range picker, then gauges and
/// charts, or the state that explains why there are none.
struct KnobHardwareContent: View {
    let input: KnobHardwareInput
    var columns = 2
    var now = Date()

    var body: some View {
        HardwarePageHeader(status: status, range: input.range, setRange: { r in input.setRange(r) },
                           pickerDisabled: input.stats.value?.diagnostics == .off,
                           liveHelp: "The knob reports every 5 seconds while this page is open.") {
            Button("Diagnostics Settings", systemImage: "slider.horizontal.3") {
                showSettings(.device(input.knobID, .hardware(.behavior)))
            }
            .labelStyle(.iconOnly)
            .buttonStyle(.borderless)
            .help("Change what the knob reports in Behavior")
        }
        content
    }

    private var status: HardwareStatus? {
        guard let s = input.stats.value else { return nil }
        if !s.online { return .offline(lastSeen: s.lastSeen) }
        return s.isLive(now: now) ? .live : .online
    }

    // MARK: Content

    @ViewBuilder private var content: some View {
        switch input.stats {
        case .loading:
            grid(KnobStatsFake.make(range: input.range, now: now), level: .full)
                .redacted(reason: .placeholder)
                .accessibilityLabel("Loading")
        case .failed(let error, nil, _):
            HardwareStateBox {
                ContentUnavailableView {
                    Label("Knob stats unavailable", systemImage: "exclamationmark.triangle")
                } description: {
                    Text(error == .featureOff ? String(localized: "Update the Ember server to see the knob's hardware stats.")
                                              : String(localized: error.message))
                }
            }
        default:
            if let s = input.stats.value { loaded(s) }
        }
    }

    @ViewBuilder private func loaded(_ s: KnobStats) -> some View {
        if s.diagnostics == .off {
            HardwareStateBox { diagnosticsOff }
        } else if s.points.isEmpty, s.latest == nil {
            HardwareStateBox {
                if s.online {
                    ContentUnavailableView {
                        Label("Waiting for the knob's first report", systemImage: "hourglass")
                    } description: {
                        Text("The knob sends hardware stats every minute, and every 5 seconds while this page is open. It needs firmware with diagnostics.")
                    }
                } else {
                    ContentUnavailableView {
                        Label("The knob is offline", systemImage: "wifi.slash")
                    } description: {
                        Text("Its stats appear here once it checks in with Ember again.")
                    }
                }
            }
        } else {
            grid(s, level: s.diagnostics)
                .overlay(alignment: .topTrailing) {
                    if let at = input.stats.loadedAt, input.stats.isStale { StaleChip(since: at) }
                }
        }
    }

    private var diagnosticsOff: some View {
        ContentUnavailableView {
            Label("Diagnostics are off", systemImage: "waveform.path.ecg")
        } description: {
            Text("Turn on diagnostics to have the knob report its processor, memory, temperature and Wi-Fi. Full diagnostics add network requests and rendering.")
        } actions: {
            HStack {
                Button("Turn On Full Diagnostics") { input.setDiagnostics(.full) }
                    .disabled(input.savingDiagnostics)
                Button("Turn On Diagnostics") { input.setDiagnostics(.basic) }
                    .buttonStyle(.borderedProminent)
                    .keyboardShortcut(.defaultAction)
                    .disabled(input.savingDiagnostics)
            }
            if let e = input.diagnosticsError {
                Label { Text(verbatim: e) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .foregroundStyle(.red)
                    .font(.callout)
            }
        }
    }

    // MARK: Cards

    private func cards(_ s: KnobStats, level: KnobDiagnostics) -> [(id: KnobCardID, size: CardSize)] {
        var out: [(id: KnobCardID, size: CardSize)] = [(.overview, .wide), (.cpu, .standard), (.memory, .standard)]
        if s.hasPSRAM { out.append((.psram, .standard)) }
        out += [(.temperature, .standard), (.wifi, .standard)]
        if level == .full { out += [(.requests, .standard), (.latency, .standard), (.rendering, .standard)] }
        return out
    }

    private func grid(_ s: KnobStats, level: KnobDiagnostics) -> some View {
        VStack(spacing: HardwareMetrics.spacing) {
            HardwareCardGrid(cards: cards(s, level: level), columns: columns) { card($0, s) }
            if level == .basic { fullDiagnosticsBanner }
        }
    }

    /// At basic level, one row offers what full adds instead of three empty cards.
    private var fullDiagnosticsBanner: some View {
        GroupBox {
            HStack(spacing: 10) {
                Image(systemName: "waveform.path.ecg")
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Text("Request and rendering stats need full diagnostics.")
                    .foregroundStyle(.secondary)
                Spacer(minLength: 8)
                Button("Use Full Diagnostics") { input.setDiagnostics(.full) }
                    .disabled(input.savingDiagnostics)
            }
            .padding(.vertical, 2)
        }
    }

    private func chart(_ title: LocalizedStringKey, ax: String, systemImage: String, _ s: KnobStats,
                       _ lines: [(HardwareLine, (KnobStats.Sample) -> Double?)], headline: Double? = nil,
                       fixedDomain: ClosedRange<Double>? = nil, zeroBaseline: Bool = false,
                       interpolation: InterpolationMethod = .linear, warn: Bool = false,
                       format: @escaping (Double) -> String) -> some View {
        let r = input.range
        return HardwareChartCard(title: title, axTitle: ax, systemImage: systemImage,
                                 points: s.series(lines.map { ($0.0.name, $0.1) }, range: r),
                                 lines: lines.map(\.0), range: r, now: now,
                                 headline: headline ?? s.latest.flatMap { l in lines.first.flatMap { $0.1(l) } },
                                 fixedDomain: fixedDomain, zeroBaseline: zeroBaseline, interpolation: interpolation,
                                 warn: warn, format: format)
    }

    @ViewBuilder
    private func card(_ id: KnobCardID, _ s: KnobStats) -> some View {
        let l = s.latest
        switch id {
        case .overview:
            HardwareNowCard(gauges: gauges(s), facts: facts(s), online: s.online, asOf: l?.t ?? s.lastSeen)
        case .cpu:
            chart("Processor", ax: String(localized: "Processor load"), systemImage: "cpu", s, cpuLines(s),
                  headline: l?.cpuAverage, fixedDomain: 0...100,
                  warn: (l?.cpuAverage ?? 0) > KnobReadout.busyCPU, format: HardwareFormat.percent)
        case .memory:
            chart("Internal memory", ax: String(localized: "Internal memory"), systemImage: "memorychip", s, [
                (HardwareLine(name: String(localized: "Free"), color: HardwarePalette.first), { $0.heapInternalFreeBytes.map(Double.init) }),
                (HardwareLine(name: String(localized: "Largest block"), color: HardwarePalette.third), { $0.heapInternalLargestBytes.map(Double.init) }),
                (HardwareLine(name: String(localized: "Lowest free"), color: HardwarePalette.second, dashed: true), { $0.heapInternalMinBytes.map(Double.init) }),
            ], zeroBaseline: true, warn: (l?.heapInternalLargestBytes ?? .max) < KnobReadout.lowLargestBlock,
                  format: HardwareFormat.bytes(scaleTo: s.points.compactMap(\.heapInternalFreeBytes).max() ?? 0))
        case .psram:
            chart("PSRAM", ax: String(localized: "PSRAM"), systemImage: "memorychip.fill", s, [
                (HardwareLine(name: String(localized: "Free"), color: HardwarePalette.first), { $0.psramFreeBytes.map(Double.init) }),
                (HardwareLine(name: String(localized: "Largest block"), color: HardwarePalette.third), { $0.psramLargestBytes.map(Double.init) }),
                (HardwareLine(name: String(localized: "Lowest free"), color: HardwarePalette.second, dashed: true), { $0.psramMinBytes.map(Double.init) }),
            ], format: HardwareFormat.bytes(scaleTo: s.points.compactMap(\.psramFreeBytes).max() ?? 0))
        case .temperature:
            chart("Chip temperature", ax: String(localized: "Chip temperature"), systemImage: "thermometer.medium", s,
                  [(HardwareLine(name: String(localized: "Temperature"), color: HardwarePalette.second), { $0.tempC })],
                  warn: (l?.tempC ?? 0) > KnobReadout.hotC, format: HardwareFormat.celsius)
        case .wifi:
            chart("Wi-Fi signal", ax: String(localized: "Wi-Fi signal"), systemImage: "wifi", s,
                  [(HardwareLine(name: String(localized: "Signal"), color: HardwarePalette.first), { $0.rssiDBm.map(Double.init) })],
                  interpolation: input.range == .day ? .linear : .stepEnd,
                  warn: l?.rssiDBm.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false, format: HardwareFormat.dbm)
        case .requests:
            chart("Requests", ax: String(localized: "Requests per minute"), systemImage: "arrow.up.arrow.down", s, [
                (HardwareLine(name: String(localized: "Requests"), color: HardwarePalette.first), { $0.requestsPerMin }),
                (HardwareLine(name: String(localized: "Failures"), color: HardwarePalette.second, dashed: true), { $0.requestFailuresPerMin }),
            ], zeroBaseline: true, warn: (l?.requestFailuresPerMin ?? 0) > 0, format: HardwareFormat.perMinute)
        case .latency:
            chart("Request latency", ax: String(localized: "Request latency"), systemImage: "stopwatch", s, [
                (HardwareLine(name: String(localized: "Average"), color: HardwarePalette.first), { $0.requestLatencyAvgMS }),
                (HardwareLine(name: String(localized: "Slowest"), color: HardwarePalette.fourth, dashed: true), { $0.requestLatencyMaxMS.map(Double.init) }),
            ], zeroBaseline: true, format: HardwareFormat.milliseconds)
        case .rendering:
            chart("Rendering", ax: String(localized: "Frame rate"), systemImage: "square.stack.3d.forward.dottedline", s,
                  [(HardwareLine(name: String(localized: "Frame rate"), color: HardwarePalette.third), { $0.renderFPS })],
                  zeroBaseline: true, format: HardwareFormat.fps)
        }
    }

    private func cpuLines(_ s: KnobStats) -> [(HardwareLine, (KnobStats.Sample) -> Double?)] {
        let cores = s.points.map { $0.cpuPercent?.count ?? 0 }.max() ?? 0
        let colors: [Color] = [HardwarePalette.first, HardwarePalette.second, HardwarePalette.third, HardwarePalette.fourth]
        return (0..<cores).map { core in
            (HardwareLine(name: String(localized: "Core \(core + 1)", comment: "Knob Hardware CPU chart: a processor core (\"Core 1\")."),
                          color: colors[core % colors.count]),
             { sample in sample.cpuPercent.flatMap { $0.indices.contains(core) ? $0[core] : nil } })
        }
    }

    // MARK: Now card

    private func gauges(_ s: KnobStats) -> [HardwareGauge] {
        let l = s.latest
        var out = [
            HardwareGauge(id: "cpu", title: "Processor", value: l?.cpuAverage, range: 0...100,
                          text: l?.cpuAverage.map(HardwareFormat.percent),
                          tint: HardwareNowCard.level(l?.cpuAverage, good: { $0 < 70 }, fair: { $0 < KnobReadout.busyCPU }),
                          warn: (l?.cpuAverage ?? 0) > KnobReadout.busyCPU),
            HardwareGauge(id: "temp", title: "Temperature", value: l?.tempC, range: 20...85,
                          text: l?.tempC.map(HardwareFormat.celsius),
                          tint: HardwareNowCard.level(l?.tempC, good: { $0 < 60 }, fair: { $0 < KnobReadout.hotC }),
                          warn: (l?.tempC ?? 0) > KnobReadout.hotC),
            HardwareNowCard.wifi(rssi: l?.rssiDBm),
        ]
        if s.diagnostics == .full {
            out.append(HardwareGauge(id: "fps", title: "Frame rate", value: l?.renderFPS, range: 0...KnobReadout.targetFPS,
                                     text: l?.renderFPS.map(HardwareFormat.fps),
                                     tint: HardwareNowCard.level(l?.renderFPS, good: { $0 >= 24 }, fair: { $0 >= 15 })))
        }
        return out
    }

    private func facts(_ s: KnobStats) -> [HardwareFact] {
        let l = s.latest
        return [
            HardwareFact(id: "free", title: "Free memory", value: l?.heapInternalFreeBytes.map { HardwareFormat.bytes(Double($0)) }),
            HardwareFact(id: "largest", title: "Largest block", value: l?.heapInternalLargestBytes.map { HardwareFormat.bytes(Double($0)) },
                         warn: (l?.heapInternalLargestBytes ?? .max) < KnobReadout.lowLargestBlock),
            HardwareFact(id: "psram", title: "PSRAM free", value: l?.psramFreeBytes.map { HardwareFormat.bytes(Double($0)) }),
            HardwareFact(id: "uptime", title: "Uptime", value: l?.uptimeSec.map { DurationText.uptime($0) }),
            HardwareFact(id: "reset", title: "Last restart", value: s.resetReason.map(Self.resetReason)),
            HardwareFact(id: "firmware", title: "Firmware", value: input.firmware),
            HardwareFact(id: "ip", title: "IP address", value: input.ipAddress),
        ]
    }

    /// The ESP-IDF reset reason the knob reported, in words.
    static func resetReason(_ raw: String) -> String {
        switch raw {
        case "poweron": String(localized: "Power on")
        case "sw": String(localized: "Restarted by software")
        case "panic": String(localized: "Crash")
        case "int_wdt", "task_wdt", "wdt": String(localized: "Watchdog")
        case "brownout": String(localized: "Low voltage")
        case "deepsleep": String(localized: "Woke from sleep")
        case "ext": String(localized: "Reset pin")
        default: raw
        }
    }
}

/// The page as Settings shows it: the knob's stats polled while the page is
/// on screen and its window visible.
struct KnobHardwarePane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var isVisible = true

    var body: some View {
        if let k = env.knob.knob {
            HardwareScrollPage { columns in
                TimelineView(.periodic(from: .now, by: 5)) { ctx in
                    VStack(alignment: .leading, spacing: HardwareMetrics.spacing) {
                        KnobHardwareContent(input: input(k), columns: columns, now: ctx.date)
                    }
                }
            }
            .background(WindowVisibilityReader(isVisible: $isVisible))
            .task(id: KnobPoll(visible: isVisible, knobID: k.id)) {
                guard isVisible else { return }
                await env.knobStats.run(deviceID: k.id)
            }
        }
    }

    private func input(_ k: KnobDevice) -> KnobHardwareInput {
        let env = env
        return KnobHardwareInput(
            knobID: k.id, firmware: k.lastCheckin?.fw.nonEmpty, ipAddress: k.lastCheckin?.ip.nonEmpty,
            stats: env.knobStats.stats, range: env.knobStats.range,
            setRange: { r in
                env.knobStats.range = r
                Task { await env.knobStats.refresh(deviceID: k.id) }
            },
            setDiagnostics: { level in Task { await env.setKnobDiagnostics(level) } },
            savingDiagnostics: env.knobDiagnosticsSaving,
            diagnosticsError: env.knobDiagnosticsError)
    }
}

private struct KnobPoll: Hashable {
    let visible: Bool
    let knobID: String
}
