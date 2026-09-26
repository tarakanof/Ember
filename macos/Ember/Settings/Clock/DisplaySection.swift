import SwiftUI
import EmberKit

/// Panel power, brightness, text and scrolling.
struct DisplaySection: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        let s = device.settings
        Section {
            // The same switch and value as the menu and the Dashboard.
            if device.supportsControlRoutes, let power = env.live.displayPower {
                Toggle("Display on", isOn: Binding(
                    get: { power },
                    set: { on in Task { await env.actions.run(.clock(.power(on))) } }))
                    .disabled(env.actions.isSettingDisplayPower)
            }
            Toggle("Automatic brightness", isOn: s.binding(\.autoBrightness, false))
            PercentSliderRow(title: "Brightness", percent: Binding(
                get: { DeviceUnits.brightnessPercent(raw: s.draft.brightness ?? 120) },
                set: { s.draft.brightness = DeviceUnits.brightnessRaw(percent: $0) }))
                .disabled(s.draft.autoBrightness ?? false)
            HexColorRow(title: "Text color", hex: s.binding(\.textColor, "#FFFFFF"))
            Toggle("Uppercase text", isOn: s.binding(\.uppercase, true))
            Picker("Scrolling", selection: s.binding(\.scroll, \.mode, ScrollMode.wrap.rawValue, empty: ScrollSettings())) {
                ForEach(ScrollMode.allCases) { m in Text(title(m)).tag(m.rawValue) }
            }
            PercentSliderRow(title: "Scroll speed",
                             percent: s.binding(\.scroll, \.speed, 100, empty: ScrollSettings()),
                             range: (10...500).including(s.draft.scroll?.speed ?? 100), step: 10)
                .disabled(s.draft.scroll?.mode == ScrollMode.static.rawValue)
            Toggle("Block button navigation", isOn: s.binding(\.blockNavigation, false))
        } header: {
            Text("Display")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Scrolling applies to text that doesn't fit the panel. A running Pomodoro may briefly override button navigation.")
                SaveErrorFooter(error: s.saveError)
                if let failure = env.actions.lastError, case .clock(.power) = failure.action {
                    Label { Text(failure.error.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                        .foregroundStyle(.red)
                }
            }
        }
    }

    private func title(_ m: ScrollMode) -> LocalizedStringKey {
        switch m {
        case .static: "Don't scroll"
        case .wrap: "Wrap around"
        case .loop: "Continuous"
        case .bounce: "Bounce"
        }
    }
}
