import SwiftUI
import EmberKit

/// What the Dashboard's knob section reads: plain values and callbacks, so
/// previews and snapshot renders use `KnobStatsFake`.
struct KnobDashboardInput {
    var knobID: String
    var name: String
    var firmware: String?
    var stats: Loadable<KnobStats>
    var range: KnobStatsRange
    var setRange: @MainActor @Sendable (KnobStatsRange) -> Void = { _ in }
    var setDiagnostics: @MainActor @Sendable (KnobDiagnostics) -> Void = { _ in }
    var savingDiagnostics = false
    var diagnosticsError: String?
}

enum KnobCardID: Hashable, Sendable {
    case overview, cpu, memory, psram, temperature, wifi, requests, latency, rendering
}

/// The knob's hardware stats: a header with the range picker, then gauges
/// and charts, or the state that explains why there are none.
struct KnobDashboardSection: View {
    let input: KnobDashboardInput
    var columns = 2
    var now = Date()

    @Environment(\.openWindow) private var openWindow

    static let anchor = "knob-dashboard"

    var body: some View {
        VStack(alignment: .leading, spacing: DashboardContent<DashboardData>.spacing) {
            header
            content
        }
        .id(Self.anchor)
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Label { Text("Knob") } icon: { Image(systemName: "dial.medium") }
                .font(.title2.weight(.semibold))
                .accessibilityAddTraits(.isHeader)
            Text(verbatim: input.name)
                .font(.title3)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            status
            Spacer(minLength: 12)
            Picker("Time range", selection: Binding(get: { [range = input.range] in range },
                                                set: { [setRange = input.setRange] r in setRange(r) })) {
                ForEach(KnobStatsRange.allCases) { r in Text(Self.title(r)).tag(r) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .disabled(input.stats.value?.diagnostics == .off)
            Button("Knob Settings", systemImage: "gearshape") {
                openSettings(.device(input.knobID, .hardware(.behavior)), using: openWindow)
            }
            .labelStyle(.iconOnly)
            .buttonStyle(.borderless)
            .help("Knob settings")
        }
    }

    static func title(_ r: KnobStatsRange) -> LocalizedStringKey {
        switch r {
        case .fifteenMinutes: "15 Minutes"
        case .hour: "1 Hour"
        case .day: "24 Hours"
        }
    }

    @ViewBuilder private var status: some View {
        if let s = input.stats.value {
            if !s.online {
                Label {
                    if let seen = s.lastSeen {
                        Text("Offline, last report \(Text(seen, format: .relative(presentation: .named)))",
                             comment: "Dashboard knob status; the argument is a relative time (\"5 minutes ago\").")
                    } else {
                        Text("Offline")
                    }
                } icon: { Image(systemName: "wifi.slash") }
                .foregroundStyle(.orange)
                .font(.callout)
            } else if s.isLive(now: now) {
                Label("Live", systemImage: "dot.radiowaves.left.and.right")
                    .foregroundStyle(.green)
                    .font(.callout)
                    .help("The knob reports every 5 seconds while this window is open.")
            } else {
                Label("Online", systemImage: "circle.fill")
                    .labelStyle(StatusDotLabelStyle())
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: Content

    @ViewBuilder private var content: some View {
        switch input.stats {
        case .loading:
            grid(KnobStatsFake.make(range: input.range, now: now), level: .full)
                .redacted(reason: .placeholder)
                .accessibilityLabel("Loading")
        case .failed(let error, nil, _):
            stateBox {
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
            stateBox { diagnosticsOff }
        } else if s.points.isEmpty, s.latest == nil {
            stateBox {
                if s.online {
                    ContentUnavailableView {
                        Label("Waiting for the knob's first report", systemImage: "hourglass")
                    } description: {
                        Text("The knob sends hardware stats every minute, and every 5 seconds while this window is open. It needs firmware with diagnostics.")
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

    private func stateBox<C: View>(@ViewBuilder _ c: () -> C) -> some View {
        GroupBox { c().frame(maxWidth: .infinity, minHeight: 220) }
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
        VStack(spacing: DashboardContent<DashboardData>.spacing) {
            ForEach(Array(DashboardLayout.runs(cards(s, level: level), columns: columns).enumerated()), id: \.offset) { _, run in
                switch run {
                case .wide(let id):
                    card(id, s)
                case .grid(let ids):
                    LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: DashboardContent<DashboardData>.spacing,
                                                                 alignment: .top), count: columns),
                              spacing: DashboardContent<DashboardData>.spacing) {
                        ForEach(ids, id: \.self) { card($0, s) }
                    }
                }
            }
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

    @ViewBuilder
    private func card(_ id: KnobCardID, _ s: KnobStats) -> some View {
        let r = input.range
        switch id {
        case .overview:
            KnobOverviewCard(stats: s, firmware: input.firmware, now: now)
        case .cpu:
            KnobChartCard(title: "Processor", axTitle: String(localized: "Processor load"), systemImage: "cpu",
                          stats: s, lines: cpuLines(s), range: r, now: now, headline: s.latest?.cpuAverage,
                          fixedDomain: 0...100,
                          warn: (s.latest?.cpuAverage ?? 0) > KnobReadout.busyCPU, format: KnobFormat.percent)
        case .memory:
            let bytes = KnobFormat.bytes(scaleTo: s.points.compactMap(\.heapInternalFreeBytes).max() ?? 0)
            KnobChartCard(title: "Internal memory", axTitle: String(localized: "Internal memory"), systemImage: "memorychip",
                          stats: s, lines: [
                              KnobLine(name: String(localized: "Free"), color: KnobPalette.first) { $0.heapInternalFreeBytes.map(Double.init) },
                              KnobLine(name: String(localized: "Largest block"), color: KnobPalette.third) { $0.heapInternalLargestBytes.map(Double.init) },
                              KnobLine(name: String(localized: "Lowest free"), color: KnobPalette.second, dashed: true) { $0.heapInternalMinBytes.map(Double.init) },
                          ], range: r, now: now, zeroBaseline: true,
                          warn: (s.latest?.heapInternalLargestBytes ?? .max) < KnobReadout.lowLargestBlock, format: bytes)
        case .psram:
            let bytes = KnobFormat.bytes(scaleTo: s.points.compactMap(\.psramFreeBytes).max() ?? 0)
            KnobChartCard(title: "PSRAM", axTitle: String(localized: "PSRAM"), systemImage: "memorychip.fill",
                          stats: s, lines: [
                              KnobLine(name: String(localized: "Free"), color: KnobPalette.first) { $0.psramFreeBytes.map(Double.init) },
                              KnobLine(name: String(localized: "Largest block"), color: KnobPalette.third) { $0.psramLargestBytes.map(Double.init) },
                              KnobLine(name: String(localized: "Lowest free"), color: KnobPalette.second, dashed: true) { $0.psramMinBytes.map(Double.init) },
                          ], range: r, now: now, format: bytes)
        case .temperature:
            KnobChartCard(title: "Chip temperature", axTitle: String(localized: "Chip temperature"), systemImage: "thermometer.medium",
                          stats: s, lines: [KnobLine(name: String(localized: "Temperature"), color: KnobPalette.second) { $0.tempC }],
                          range: r, now: now, warn: (s.latest?.tempC ?? 0) > KnobReadout.hotC, format: KnobFormat.celsius)
        case .wifi:
            KnobChartCard(title: "Wi-Fi signal", axTitle: String(localized: "Wi-Fi signal"), systemImage: "wifi",
                          stats: s, lines: [KnobLine(name: String(localized: "Signal"), color: KnobPalette.first) { $0.rssiDBm.map(Double.init) }],
                          range: r, now: now, interpolation: r == .day ? .linear : .stepEnd,
                          warn: s.latest?.rssiDBm.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false, format: KnobFormat.dbm)
        case .requests:
            KnobChartCard(title: "Requests", axTitle: String(localized: "Requests per minute"), systemImage: "arrow.up.arrow.down",
                          stats: s, lines: [
                              KnobLine(name: String(localized: "Requests"), color: KnobPalette.first) { $0.requestsPerMin },
                              KnobLine(name: String(localized: "Failures"), color: KnobPalette.second, dashed: true) { $0.requestFailuresPerMin },
                          ], range: r, now: now, zeroBaseline: true,
                          warn: (s.latest?.requestFailuresPerMin ?? 0) > 0, format: KnobFormat.perMinute)
        case .latency:
            KnobChartCard(title: "Request latency", axTitle: String(localized: "Request latency"), systemImage: "stopwatch",
                          stats: s, lines: [
                              KnobLine(name: String(localized: "Average"), color: KnobPalette.first) { $0.requestLatencyAvgMS },
                              KnobLine(name: String(localized: "Slowest"), color: KnobPalette.fourth, dashed: true) { $0.requestLatencyMaxMS.map(Double.init) },
                          ], range: r, now: now, zeroBaseline: true, format: KnobFormat.milliseconds)
        case .rendering:
            KnobChartCard(title: "Rendering", axTitle: String(localized: "Frame rate"), systemImage: "square.stack.3d.forward.dottedline",
                          stats: s, lines: [KnobLine(name: String(localized: "Frame rate"), color: KnobPalette.third) { $0.renderFPS }],
                          range: r, now: now, zeroBaseline: true, format: KnobFormat.fps)
        }
    }

    private func cpuLines(_ s: KnobStats) -> [KnobLine] {
        let cores = s.points.map { $0.cpuPercent?.count ?? 0 }.max() ?? 0
        let colors: [Color] = [KnobPalette.first, KnobPalette.second, KnobPalette.third, KnobPalette.fourth]
        return (0..<cores).map { core in
            KnobLine(name: String(localized: "Core \(core + 1)", comment: "Dashboard knob CPU chart: a processor core (\"Core 1\")."),
                     color: colors[core % colors.count]) { sample in
                sample.cpuPercent.flatMap { $0.indices.contains(core) ? $0[core] : nil }
            }
        }
    }
}

private struct StatusDotLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 4) {
            configuration.icon.font(.system(size: 7)).foregroundStyle(.green)
            configuration.title
        }
    }
}

/// Value formats for the knob's readings.
enum KnobFormat {
    static func percent(_ v: Double) -> String { Percent.text(v) }

    /// Bytes in decimal units (matching the axis's decimal ticks), in the
    /// unit the knob's numbers usually need.
    static func bytes(_ v: Double) -> String {
        bytes(scaleTo: v)(v)
    }

    /// A byte format fixed to one unit for a whole chart, so its axis reads
    /// "0 KB, 20 KB, 40 KB" rather than mixing bytes and KB.
    static func bytes(scaleTo largest: Int) -> (Double) -> String { bytes(scaleTo: Double(largest)) }

    static func bytes(scaleTo largest: Double) -> (Double) -> String {
        let f = ByteCountFormatter()
        f.countStyle = .decimal
        f.allowsNonnumericFormatting = false
        f.allowedUnits = largest >= 1_000_000 ? .useMB : .useKB
        return { v in f.string(fromByteCount: Int64(v.rounded())) }
    }

    static func celsius(_ v: Double) -> String {
        Measurement(value: v, unit: UnitTemperature.celsius)
            .formatted(.measurement(width: .narrow, usage: .asProvided, numberFormatStyle: .number.precision(.fractionLength(0...1))))
    }

    static func dbm(_ v: Double) -> String {
        String(localized: "\(Int(v.rounded())) dBm", comment: "Wi-Fi signal strength in decibel-milliwatts (\"-62 dBm\").")
    }

    static func perMinute(_ v: Double) -> String {
        String(localized: "\(v.formatted(.number.precision(.fractionLength(0...1))))/min",
               comment: "A rate per minute (\"31/min\").")
    }

    static func milliseconds(_ v: Double) -> String {
        String(localized: "\(Int(v.rounded())) ms", comment: "A duration in milliseconds (\"38 ms\").")
    }

    static func fps(_ v: Double) -> String {
        String(localized: "\(v.formatted(.number.precision(.fractionLength(0...1)))) fps",
               comment: "Frames per second (\"29.5 fps\").")
    }
}
