import SwiftUI
import EmberKit

/// Card 11: the clock's own telemetry and how reliably the server reaches it.
struct ClockHealthCard: View {
    let health: Loadable<ClockHealth>
    var webURL: URL?
    var now = Date()
    @Environment(\.openURL) private var openURL

    var body: some View {
        DashboardCard(title: "Clock health", systemImage: "stethoscope") {
            FeedStateView(feed: health, placeholder: DashboardPlaceholders.clockHealth, isEmpty: { $0.device == nil },
                          emptyTitle: "No clock configured", emptySymbol: "clock.badge.questionmark",
                          offTitle: ServerRequirement.title, offSymbol: ServerRequirement.symbol) { h in
                content(h)
            }
        } accessory: {
            if let d = health.value?.device, !d.reachable {
                Label("Unreachable", systemImage: "wifi.slash").foregroundStyle(.orange)
            } else if let url = webURL {
                Button("Clock Web UI", systemImage: "arrow.up.right.square") { openURL(url) }
                    .buttonStyle(.link)
                    .labelStyle(.titleOnly)
                    .font(.callout)
            }
        }
    }

    private func content(_ h: ClockHealth) -> some View {
        let d = h.device
        return Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 14, verticalSpacing: 7) {
            GridRow {
                cell("Battery", symbol: d?.batteryPercent.map { ClockHealthReadout.batterySymbol(percent: $0) } ?? "battery.0",
                     value: d?.batteryPercent.map { Percent.text($0) },
                     warn: d?.lowBattery == true)
                cell("Wi-Fi", symbol: "wifi", variable: d?.wifiRssiDbm.map { ClockHealthReadout.wifi(rssi: $0).strength },
                     value: d?.wifiRssiDbm.map { "\($0) dBm" },
                     warn: d?.wifiRssiDbm.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false)
            }
            GridRow {
                cell("Temperature", symbol: "thermometer.medium", value: climate(d))
                cell("Uptime", symbol: "power", value: d?.uptimeSec.map { DurationText.uptime($0) })
            }
            GridRow {
                firmware(h)
                publish(h.publish)
            }
            GridRow {
                cell("Last publish", symbol: "paperplane", value: h.publish.lastAt.map { RelativeText.short($0, now: now) },
                     warn: h.publish.lastAt != nil && !h.publish.lastOk)
                cell("Showing", symbol: "rectangle.on.rectangle",
                     value: d?.currentApp.flatMap { $0.isEmpty ? nil : String(localized: AppNames.display($0)) })
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func climate(_ d: ClockHealth.Device?) -> String? {
        guard let t = d?.temperatureC else { return nil }
        let temp = Measurement(value: t, unit: UnitTemperature.celsius)
            .formatted(.measurement(width: .narrow, usage: .asProvided, numberFormatStyle: .number.precision(.fractionLength(1))))
        guard let h = d?.humidityPercent else { return temp }
        return "\(temp) · \(Percent.text(h))"
    }

    @ViewBuilder
    private func firmware(_ h: ClockHealth) -> some View {
        if h.updateAvailable == true, let latest = h.latestFirmware {
            cell("Firmware", symbol: "arrow.down.circle", value: h.device?.firmware,
                 note: String(localized: "\(latest) available"), warn: false)
        } else {
            cell("Firmware", symbol: "cpu", value: h.device?.firmware)
        }
    }

    private func publish(_ p: ClockHealth.Publish) -> some View {
        let ratio = ClockHealthReadout.publishRatio(p)
        return cell("Delivered (24 h)", symbol: "checkmark.circle", value: ratio.map { Percent.text(ratio: $0) },
                    note: ratio == nil ? nil : String(localized: "\(p.ok24h) of \(p.ok24h + p.fail24h)"),
                    warn: ratio.map(ClockHealthReadout.publishIsPoor) ?? false)
    }

    private func cell(_ title: LocalizedStringKey, symbol: String, variable: Double? = nil, value: String?,
                      note: String? = nil, warn: Bool = false) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Image(systemName: symbol, variableValue: variable)
                .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.secondary))
                .frame(width: 18)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .firstTextBaseline, spacing: 4) {
                    Text(value ?? "—")
                        .font(.body.weight(.medium))
                        .monospacedDigit()
                        .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.primary))
                    if let note {
                        Text(note).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Text(title).font(.caption).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue([value ?? String(localized: "Not available"), note].compactMap { $0 }.joined(separator: ", "))
    }
}
