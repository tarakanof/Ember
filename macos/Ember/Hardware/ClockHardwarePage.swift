import Charts
import SwiftUI
import EmberKit

struct ClockHardwareInput {
    var stats: Loadable<ClockStats>
    var health: ClockHealth?
    var range: HardwareRange
    var setRange: @MainActor @Sendable (HardwareRange) -> Void = { _ in }
    var webURL: URL?
}

enum ClockCardID: Hashable, Sendable {
    case overview, wifi, memory, temperature, humidity, light, publishing
}

struct ClockHardwareContent: View {
    let input: ClockHardwareInput
    var columns = 2
    var now = Date()

    @Environment(\.openURL) private var openURL

    var body: some View {
        HardwarePageHeader(status: status, range: input.range, setRange: { r in input.setRange(r) },
                           pickerDisabled: input.stats.value?.configured == false) {
            if let url = input.webURL {
                Button("Clock Web UI", systemImage: "arrow.up.right.square") { openURL(url) }
                    .labelStyle(.iconOnly)
                    .buttonStyle(.borderless)
                    .help("Open the clock's own web page")
            }
        }
        content
    }

    private var status: HardwareStatus? {
        guard let s = input.stats.value, s.configured, let reachable = s.reachable else { return nil }
        return reachable ? .online : .offline(lastSeen: s.latest?.t)
    }

    @ViewBuilder private var content: some View {
        switch input.stats {
        case .loading:
            grid(ClockStatsFake.make(range: input.range, now: now))
                .redacted(reason: .placeholder)
                .accessibilityLabel("Loading")
        case .failed(let error, nil, _):
            HardwareStateBox {
                ContentUnavailableView {
                    Label("Clock stats unavailable", systemImage: "exclamationmark.triangle")
                } description: {
                    Text(error == .featureOff ? String(localized: "Update the Ember server to see the clock's hardware stats.")
                                              : String(localized: error.message))
                }
            }
        default:
            if let s = input.stats.value { loaded(s) }
        }
    }

    @ViewBuilder private func loaded(_ s: ClockStats) -> some View {
        if !s.configured {
            HardwareStateBox {
                ContentUnavailableView {
                    Label("No clock configured", systemImage: "clock.badge.questionmark")
                } description: {
                    Text("Give Ember the clock's address, or discover it, under Status.")
                } actions: {
                    Button("Open Status") { showSettings(.device(clockDeviceID, .hardware(.status))) }
                }
            }
        } else if s.latest == nil {
            HardwareStateBox {
                if s.reachable == false {
                    ContentUnavailableView {
                        Label("The clock is unreachable", systemImage: "wifi.slash")
                    } description: {
                        Text("Its readings appear here once Ember reaches it again.")
                    }
                } else {
                    ContentUnavailableView {
                        Label("Waiting for the clock's first reading", systemImage: "hourglass")
                    } description: {
                        Text("Ember reads the clock every 30 seconds and keeps the last 24 hours while the server runs.")
                    }
                }
            }
        } else {
            grid(s)
                .overlay(alignment: .topTrailing) {
                    if let at = input.stats.loadedAt, input.stats.isStale { StaleChip(since: at) }
                }
        }
    }

    private func cards(_ s: ClockStats) -> [(id: ClockCardID, size: CardSize)] {
        var out: [(id: ClockCardID, size: CardSize)] = [(.overview, .wide), (.wifi, .standard), (.memory, .standard)]
        if s.has(\.temperatureC) { out.append((.temperature, .standard)) }
        if s.has(\.humidityPercent) { out.append((.humidity, .standard)) }
        if s.has(\.lightLux) { out.append((.light, .standard)) }
        out.append((.publishing, .wide))
        return out
    }

    private func grid(_ s: ClockStats) -> some View {
        HardwareCardGrid(cards: cards(s), columns: columns) { card($0, s) }
    }

    private func chart(_ title: LocalizedStringKey, ax: String, systemImage: String, _ s: ClockStats,
                       _ lines: [(HardwareLine, (ClockStats.Sample) -> Double?)],
                       fixedDomain: ClosedRange<Double>? = nil, zeroBaseline: Bool = false,
                       interpolation: InterpolationMethod = .linear, warn: Bool = false,
                       format: @escaping (Double) -> String) -> some View {
        let r = input.range
        return HardwareChartCard(title: title, axTitle: ax, systemImage: systemImage,
                                 points: s.series(lines.map { ($0.0.name, $0.1) }, range: r),
                                 lines: lines.map(\.0), range: r, now: now,
                                 headline: s.latest.flatMap { l in lines.first.flatMap { $0.1(l) } },
                                 fixedDomain: fixedDomain, zeroBaseline: zeroBaseline, interpolation: interpolation,
                                 warn: warn, format: format)
    }

    @ViewBuilder
    private func card(_ id: ClockCardID, _ s: ClockStats) -> some View {
        let l = s.latest
        let stepped: InterpolationMethod = input.range == .day ? .linear : .stepEnd
        switch id {
        case .overview:
            HardwareNowCard(gauges: gauges(s), facts: facts(s), online: s.online, asOf: l?.t)
        case .wifi:
            chart("Wi-Fi signal", ax: String(localized: "Wi-Fi signal"), systemImage: "wifi", s,
                  [(HardwareLine(name: String(localized: "Signal"), color: HardwarePalette.first), { $0.rssiDBm.map(Double.init) })],
                  interpolation: stepped,
                  warn: l?.rssiDBm.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false, format: HardwareFormat.dbm)
        case .memory:
            chart("Memory", ax: String(localized: "Free memory"), systemImage: "memorychip", s, [
                (HardwareLine(name: String(localized: "Free"), color: HardwarePalette.first), { $0.freeHeapBytes.map(Double.init) }),
                (HardwareLine(name: String(localized: "Lowest free"), color: HardwarePalette.second, dashed: true), { $0.minFreeHeapBytes.map(Double.init) }),
            ], zeroBaseline: true, warn: (l?.freeHeapBytes ?? .max) < ClockReadout.lowHeap,
                  format: HardwareFormat.bytes(scaleTo: s.points.compactMap(\.freeHeapBytes).max() ?? 0))
        case .temperature:
            chart("Temperature", ax: String(localized: "Temperature"), systemImage: "thermometer.medium", s,
                  [(HardwareLine(name: String(localized: "Temperature"), color: HardwarePalette.second), { $0.temperatureC })],
                  warn: (l?.temperatureC ?? 0) > ClockReadout.hotC, format: HardwareFormat.celsius)
        case .humidity:
            chart("Humidity", ax: String(localized: "Humidity"), systemImage: "humidity", s,
                  [(HardwareLine(name: String(localized: "Humidity"), color: HardwarePalette.third), { $0.humidityPercent })],
                  format: HardwareFormat.percent)
        case .light:
            chart("Light level", ax: String(localized: "Light level"), systemImage: "light.max", s,
                  [(HardwareLine(name: String(localized: "Light"), color: HardwarePalette.first), { $0.lightLux })],
                  zeroBaseline: true, format: HardwareFormat.lux)
        case .publishing:
            publishing(s)
        }
    }

    private func publishing(_ s: ClockStats) -> some View {
        let unit: Calendar.Component = input.range == .day ? .hour : .minute
        let delivered = HardwareLine(name: String(localized: "Delivered"), color: HardwarePalette.first)
        let failed = HardwareLine(name: String(localized: "Failed"), color: HardwarePalette.second)
        let raw = s.series([(delivered.name, { Double($0.publishOK) }), (failed.name, { Double($0.publishFail) })],
                           range: input.range)
        let points = HardwareSeries.summed(raw.filter { $0.value > 0 }, per: unit)
        let (ok, fail) = Self.publishes(s)
        let text = fail > 0 ? String(localized: "\(fail) failed",
                                     comment: "Clock Hardware publishing chart: publishes that didn't reach the clock in the selected range (\"12 failed\").") : nil
        let poor = ok + fail > 0 && ClockHealthReadout.publishIsPoor(Double(ok) / Double(ok + fail))
        return HardwareChartCard(title: "Publishing", axTitle: String(localized: "Publishes"),
                                 systemImage: "paperplane", points: points, lines: [delivered, failed],
                                 range: input.range, now: now, headlineText: text, style: .bars(unit),
                                 warn: poor, format: HardwareFormat.count)
    }

    static func publishes(_ s: ClockStats) -> (ok: Int, fail: Int) {
        (s.points.reduce(0) { $0 + $1.publishOK }, s.points.reduce(0) { $0 + $1.publishFail })
    }

    private func gauges(_ s: ClockStats) -> [HardwareGauge] {
        let l = s.latest
        var out = [HardwareNowCard.wifi(rssi: l?.rssiDBm)]
        let heap = l?.freeHeapBytes.map(Double.init)
        out.append(HardwareGauge(id: "heap", title: "Memory", value: heap, range: 0...160_000,
                                 text: heap.map(HardwareFormat.bytes),
                                 tint: HardwareNowCard.level(heap, good: { $0 >= Double(ClockReadout.fairHeap) },
                                                             fair: { $0 >= Double(ClockReadout.lowHeap) }),
                                 warn: (l?.freeHeapBytes ?? .max) < ClockReadout.lowHeap))
        if s.has(\.temperatureC) {
            out.append(HardwareGauge(id: "temp", title: "Temperature", value: l?.temperatureC, range: 0...50,
                                     text: l?.temperatureC.map(HardwareFormat.celsius),
                                     tint: HardwareNowCard.level(l?.temperatureC, good: { $0 < 35 }, fair: { $0 <= ClockReadout.hotC }),
                                     warn: (l?.temperatureC ?? 0) > ClockReadout.hotC))
        }
        if s.has(\.humidityPercent) {
            out.append(HardwareGauge(id: "humidity", title: "Humidity", value: l?.humidityPercent, range: 0...100,
                                     text: l?.humidityPercent.map(HardwareFormat.percent),
                                     tint: HardwareNowCard.level(l?.humidityPercent, good: { (30...60).contains($0) },
                                                                 fair: { (20...70).contains($0) })))
        }
        if s.has(\.lightLux) {
            out.append(HardwareGauge(id: "light", title: "Light", value: l?.lightLux, range: 0...200,
                                     text: l?.lightLux.map(HardwareFormat.lux), tint: .blue))
        }
        if s.has(\.batteryPercent) {
            let low = input.health?.device?.lowBattery == true || (l?.batteryPercent ?? 100) < ClockReadout.lowBattery
            out.append(HardwareGauge(id: "battery", title: "Battery", value: l?.batteryPercent, range: 0...100,
                                     text: l?.batteryPercent.map(HardwareFormat.percent),
                                     tint: low ? .orange : HardwareNowCard.level(l?.batteryPercent, good: { $0 >= 50 }, fair: { _ in true }),
                                     warn: low))
        }
        return out
    }

    private func facts(_ s: ClockStats) -> [HardwareFact] {
        let h = input.health
        let d = h?.device
        let (ok, fail) = Self.publishes(s)
        return [
            HardwareFact(id: "uptime", title: "Uptime", value: d?.uptimeSec.map { DurationText.uptime($0) }),
            HardwareFact(id: "firmware", title: "Firmware", value: d?.firmware,
                         note: h?.updateAvailable == true ? h?.latestFirmware.map {
                             String(localized: "\($0) available", comment: "Clock firmware fact: a newer release (\"1.1.2 available\").")
                         } : nil),
            HardwareFact(id: "app", title: "Current app", value: d?.currentApp),
            HardwareFact(id: "ip", title: "IP address", value: s.ipAddress),
            HardwareFact(id: "reset", title: "Last restart", value: d?.resetReason),
            HardwareFact(id: "delivered", title: "Delivered", value: ClockReadout.delivered(ok: ok, fail: fail),
                         note: ok + fail > 0 ? String(localized: "\(ok) of \(ok + fail)") : nil,
                         warn: ok + fail > 0 && ClockHealthReadout.publishIsPoor(Double(ok) / Double(ok + fail))),
        ]
    }
}

struct ClockHardwarePane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var isVisible = true
    @State private var webURL: URL?

    var body: some View {
        HardwareScrollPage { columns in
            TimelineView(.periodic(from: .now, by: 15)) { ctx in
                VStack(alignment: .leading, spacing: HardwareMetrics.spacing) {
                    ClockHardwareContent(input: input, columns: columns, now: ctx.date)
                }
            }
        }
        .background(WindowVisibilityReader(isVisible: $isVisible))
        .task(id: isVisible) {
            guard isVisible else { return }
            await env.clockStats.run()
        }
        .task(id: env.serverURL) { webURL = try? await env.connection.device.config().webURL }
    }

    private var input: ClockHardwareInput {
        let env = env
        return ClockHardwareInput(
            stats: env.clockStats.stats, health: env.live.clockHealth.value, range: env.clockStats.range,
            setRange: { r in
                env.clockStats.range = r
                Task { await env.clockStats.refresh() }
            },
            webURL: webURL)
    }
}
