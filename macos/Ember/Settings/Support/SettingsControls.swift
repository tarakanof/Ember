import SwiftUI
import EmberKit

struct StepperRow: View {
    let title: LocalizedStringKey
    @Binding var value: Int
    let range: ClosedRange<Int>
    var step: Int = 1
    var info: SettingsInfo? = nil
    let valueText: (Int) -> Text

    var body: some View {
        InfoRow(title, info: info) { label in
            LabeledContent {
                HStack(spacing: 6) {
                    valueText(value).monospacedDigit()
                    Stepper(value: $value, in: range, step: step) { Text(title) }
                        .labelsHidden()
                }
            } label: {
                label
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAdjustableAction { direction in
            switch direction {
            case .increment: value = min(value + step, range.upperBound)
            case .decrement: value = max(value - step, range.lowerBound)
            @unknown default: break
            }
        }
    }
}

struct DecimalStepperRow: View {
    let title: LocalizedStringKey
    @Binding var value: Double
    let range: ClosedRange<Double>
    let step: Double
    var info: SettingsInfo? = nil
    let valueText: (Double) -> Text

    var body: some View {
        InfoRow(title, info: info) { label in
            LabeledContent {
                HStack(spacing: 6) {
                    valueText(value).monospacedDigit()
                    Stepper(value: $value, in: range, step: step) { Text(title) }
                        .labelsHidden()
                }
            } label: {
                label
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAdjustableAction { direction in
            switch direction {
            case .increment: value = min(value + step, range.upperBound)
            case .decrement: value = max(value - step, range.lowerBound)
            @unknown default: break
            }
        }
    }
}

struct PercentSliderRow: View {
    let title: LocalizedStringKey
    @Binding var percent: Int
    var range: ClosedRange<Int> = 0...100
    var step: Int = 1
    var info: SettingsInfo? = nil

    var body: some View {
        InfoRow(title, info: info) { label in
            sliderRow(label)
        }
    }

    private func sliderRow(_ label: InfoLabel) -> some View {
        LabeledContent {
            HStack(spacing: 8) {
                Slider(value: Binding(get: { Double(percent) }, set: { percent = Int($0.rounded()) }),
                       in: Double(range.lowerBound)...Double(range.upperBound), step: Double(step)) {
                    Text(title)
                }
                .labelsHidden()
                .frame(maxWidth: 220)
                .accessibilityValue(percentText)
                percentText
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
            }
        } label: {
            label
        }
    }

    private var percentText: Text {
        Text(Double(percent) / 100, format: .percent.precision(.fractionLength(0)))
    }
}

struct HexColorRow: View {
    let title: LocalizedStringKey
    @Binding var hex: String
    var fallback: String = "#FFFFFF"

    var body: some View {
        ColorPicker(selection: Binding(
            get: { Color(RGB(hex: hex) ?? RGB(hex: fallback) ?? RGB(r: 255, g: 255, b: 255)) },
            set: { hex = RGB($0).hex }
        ), supportsOpacity: false) {
            Text(title)
        }
        .accessibilityValue(Text(verbatim: hex.uppercased()))
    }
}

struct InheritableColorRow: View {
    let title: LocalizedStringKey
    @Binding var hex: String?
    let inherited: String
    let supportsInherit: Bool

    var body: some View {
        if supportsInherit {
            LabeledContent {
                HStack(spacing: 8) {
                    Picker(selection: Binding(
                        get: { hex != nil },
                        set: { custom in hex = custom ? (hex ?? inherited) : nil }
                    )) {
                        Text("Same as text").tag(false)
                        Text("Custom").tag(true)
                    } label: {
                        Text(title)
                    }
                    .labelsHidden()
                    .fixedSize()
                    ColorPicker(selection: Binding(
                        get: { Color(RGB(hex: hex ?? inherited) ?? RGB(r: 255, g: 255, b: 255)) },
                        set: { hex = RGB($0).hex }
                    ), supportsOpacity: false) {
                        Text(title)
                    }
                    .labelsHidden()
                    .disabled(hex == nil)
                    .accessibilityValue(Text(verbatim: (hex ?? inherited).uppercased()))
                }
            } label: {
                Text(title)
            }
        } else {
            HexColorRow(title: title, hex: Binding(get: { hex ?? inherited }, set: { hex = $0 }))
        }
    }
}

struct SaveErrorFooter: View {
    let error: FeedError?

    var body: some View {
        if let error {
            Label { Text(error.saveMessage) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                .foregroundStyle(.red)
        }
    }
}

struct SectionFooter: View {
    var text: LocalizedStringKey?
    var error: FeedError?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if let text { Text(text) }
            SaveErrorFooter(error: error)
        }
    }
}

struct LoadStateSection: View {
    let isLoaded: Bool
    let error: FeedError?
    var offMessage: LocalizedStringKey = "This server doesn't support these settings. Update the Ember server."
    let retry: () async -> Void

    var body: some View {
        if !isLoaded {
            Section {
                if let error {
                    LabeledContent {
                        Button("Try Again") { Task { await retry() } }
                    } label: {
                        Label {
                            if error == .featureOff { Text(offMessage) } else { Text(error.message) }
                        } icon: {
                            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        }
                    }
                } else {
                    LabeledContent {
                        ProgressView().controlSize(.small)
                    } label: {
                        Text("Loading…").foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}

extension View {
    func autosaves<T: Equatable & Sendable>(_ model: ConfigModel<T>) -> some View {
        onChange(of: model.draft) { _, _ in model.scheduleSave() }
    }

    func reloads(_ load: @escaping @MainActor () async -> Void) -> some View {
        modifier(ReloadOnActivate(load: load))
    }
}

private struct ReloadOnActivate: ViewModifier {
    let load: @MainActor () async -> Void
    @Environment(\.appearsActive) private var appearsActive

    func body(content: Content) -> some View {
        content
            .task { await load() }
            .onChange(of: appearsActive) { _, active in
                if active { Task { await load() } }
            }
    }
}

func hourMinuteBinding(_ hhmm: Binding<String>) -> Binding<Date> {
    Binding(
        get: {
            let parts = hhmm.wrappedValue.split(separator: ":").compactMap { Int($0) }
            var c = DateComponents()
            c.hour = parts.first ?? 0
            c.minute = parts.count > 1 ? parts[1] : 0
            return Calendar.current.date(from: c) ?? .distantPast
        },
        set: { date in
            let c = Calendar.current.dateComponents([.hour, .minute], from: date)
            hhmm.wrappedValue = String(format: "%02d:%02d", c.hour ?? 0, c.minute ?? 0)
        })
}

func openSystemSettings(_ url: String) {
    if let url = URL(string: url) { NSWorkspace.shared.open(url) }
}

/// Wi-Fi signal in words, for the Clock and Knob status rows.
func wifiQuality(_ rssi: Int) -> LocalizedStringKey {
    if rssi >= -60 { return "Strong" }
    if rssi >= -70 { return "Good" }
    if rssi >= -80 { return "Weak" }
    return "Very weak"
}

/// "Weak · -80 dBm".
func wifiSignalText(_ rssi: Int) -> Text {
    Text("\(Text(wifiQuality(rssi))) · \(rssi) dBm")
}
