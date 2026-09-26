import Charts
import SwiftUI
import EmberKit

/// Card 12: the server's cached weather: what the clock shows, bigger.
struct WeatherCard: View {
    let weather: Loadable<WeatherState>
    var now = Date()

    var body: some View {
        DashboardCard(title: "Weather", systemImage: "cloud.sun") {
            FeedStateView(feed: weather, placeholder: DashboardPlaceholders.weather, isEmpty: { $0.current == nil },
                          emptyTitle: "No observation yet", emptySymbol: "cloud.sun",
                          offTitle: ServerRequirement.title, offSymbol: ServerRequirement.symbol) { w in
                content(w)
            }
        } accessory: {
            if let at = weather.value?.current?.fetchedAt {
                Text("updated \(RelativeText.short(at, now: now))")
            }
        }
    }

    @ViewBuilder
    private func content(_ w: WeatherState) -> some View {
        if let cur = w.current {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .center, spacing: 14) {
                    Image(systemName: w.sfSymbol(at: now))
                        .symbolRenderingMode(.multicolor)
                        .font(.largeTitle)
                        .imageScale(.large)
                        .frame(width: 52)
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 0) {
                        Text(temperature(cur.tempC, w.units))
                            .font(.largeTitle.weight(.medium))
                            .monospacedDigit()
                        HStack(spacing: 6) {
                            Text(conditionName(cur.condition))
                            if let place = w.locationName, !place.isEmpty {
                                Text(verbatim: "·")
                                Text(verbatim: place).lineLimit(1)
                            }
                        }
                        .font(.callout)
                        .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 0)
                }
                .accessibilityElement(children: .combine)
                forecast(cur, units: w.units)
                HStack(spacing: 14) {
                    if let air = w.air { airChip(air) }
                    if let sun = w.sun {
                        Label { Text(sun.sunrise, format: .dateTime.hour().minute()) } icon: { Image(systemName: "sunrise") }
                        Label { Text(sun.sunset, format: .dateTime.hour().minute()) } icon: { Image(systemName: "sunset") }
                    }
                }
                .font(.callout)
                .foregroundStyle(.secondary)
                .labelStyle(.titleAndIcon)
            }
        }
    }

    @ViewBuilder
    private func forecast(_ cur: WeatherState.Current, units: String) -> some View {
        let points = WeatherReadout.upcomingHours(cur.hourly, from: now)
        if !points.isEmpty {
            let temps = points.map { WeatherReadout.temperature(celsius: $0.tempC, units: units).value }
            let lo = (temps.min() ?? 0) - 1, hi = (temps.max() ?? 0) + 1
            Chart(points) { p in
                LineMark(x: .value("Hour", p.time), y: .value("Temperature", WeatherReadout.temperature(celsius: p.tempC, units: units).value))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(.orange)
                AreaMark(x: .value("Hour", p.time), yStart: .value("Low", lo),
                         yEnd: .value("Temperature", WeatherReadout.temperature(celsius: p.tempC, units: units).value))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(LinearGradient(colors: [.orange.opacity(0.22), .orange.opacity(0.0)],
                                                    startPoint: .top, endPoint: .bottom))
            }
            .chartYScale(domain: lo...hi)
            .chartYAxis(.hidden)
            .chartXAxis {
                AxisMarks(values: .stride(by: .hour, count: 3)) { _ in
                    AxisValueLabel(format: .dateTime.hour())
                }
            }
            .frame(height: 44)
            .accessibilityLabel("Temperature, next hours")
            .accessibilityValue(Text("\(temperature(cur.tempC, units)) now, \(temperature(points.map(\.tempC).max() ?? cur.tempC, units)) high"))
        } else {
            Spacer(minLength: 0)
        }
    }

    private func airChip(_ air: WeatherState.Air) -> some View {
        let level = WeatherReadout.AirLevel(europeanAQI: air.europeanAqi)
        return Label {
            Text("AQI \(Int(air.europeanAqi.rounded())) \(Text(airName(level)))")
        } icon: {
            Circle().fill(airColor(level)).frame(width: 8, height: 8)
        }
        .accessibilityElement(children: .combine)
    }

    private func temperature(_ c: Double, _ units: String) -> String {
        WeatherReadout.temperature(celsius: c, units: units)
            .formatted(.measurement(width: .narrow, usage: .asProvided, numberFormatStyle: .number.precision(.fractionLength(0))))
    }

    private func conditionName(_ c: String) -> LocalizedStringKey {
        switch c {
        case "clear": "Clear"
        case "clouds": "Cloudy"
        case "fog": "Fog"
        case "rain": "Rain"
        case "snow": "Snow"
        case "storm": "Thunderstorm"
        default: "Unknown"
        }
    }

    private func airName(_ l: WeatherReadout.AirLevel) -> LocalizedStringKey {
        switch l {
        case .good: "good"
        case .fair: "fair"
        case .moderate: "moderate"
        case .poor: "poor"
        case .veryPoor: "very poor"
        case .extremelyPoor: "extremely poor"
        }
    }

    private func airColor(_ l: WeatherReadout.AirLevel) -> Color {
        switch l {
        case .good, .fair: .green
        case .moderate: .yellow
        case .poor: .orange
        case .veryPoor, .extremelyPoor: .red
        }
    }
}
