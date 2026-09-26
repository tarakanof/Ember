import SwiftUI
import EmberKit

/// Offsets for the clock's temperature and humidity sensor. Auto-applied;
/// the clock picks them up within seconds, no restart.
struct SensorsSection: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        let m = device.sensors
        let measuredT = env.live.clockHealth.value?.device?.temperatureC ?? device.stats?.temperature
        let measuredH = env.live.clockHealth.value?.device?.humidityPercent ?? device.stats?.humidity
        Section {
            DecimalStepperRow(title: "Temperature offset", value: Binding(
                get: { m.draft.tempOffset ?? DeviceUnits.firmwareTemperatureOffset },
                set: { m.draft.tempOffset = $0 }),
                range: DeviceUnits.temperatureOffsetRange, step: 0.5) { v in
                Text(Measurement(value: v, unit: UnitTemperature.celsius),
                     format: .measurement(width: .abbreviated, usage: .asProvided,
                                          numberFormatStyle: .number.precision(.fractionLength(1)).sign(strategy: .always())))
            }
            DecimalStepperRow(title: "Humidity offset", value: Binding(
                get: { m.draft.humOffset ?? DeviceUnits.firmwareHumidityOffset },
                set: { m.draft.humOffset = $0 }),
                range: DeviceUnits.humidityOffsetRange, step: 1) { v in
                Text("\(v, format: .number.precision(.fractionLength(0)).sign(strategy: .always()))%")
            }
            if measuredT != nil || measuredH != nil {
                LabeledContent("Measured now") {
                    Text(measuredText(measuredT, measuredH))
                        .monospacedDigit()
                }
            }
            LabeledContent {
                Button("Reset to Firmware Defaults") { m.draft = SensorCalibration() }
                    .disabled((m.draft.tempOffset ?? DeviceUnits.firmwareTemperatureOffset) == DeviceUnits.firmwareTemperatureOffset
                              && (m.draft.humOffset ?? DeviceUnits.firmwareHumidityOffset) == DeviceUnits.firmwareHumidityOffset)
            } label: {
                EmptyView()
            }
        } header: {
            Text("Sensor Calibration")
        } footer: {
            SectionFooter(text: "The firmware subtracts 9 °C to make up for the clock warming itself. Compare the reading with a thermometer you trust and adjust by the difference.",
                          error: m.saveError)
        }
        .disabled(!m.isLoaded)
    }

    private func measuredText(_ t: Double?, _ h: Double?) -> String {
        var parts: [String] = []
        if let t {
            parts.append(Measurement(value: t, unit: UnitTemperature.celsius).formatted(
                .measurement(width: .abbreviated, usage: .asProvided,
                             numberFormatStyle: .number.precision(.fractionLength(1)))))
        }
        if let h { parts.append(Percent.text(h)) }
        return parts.joined(separator: " · ")
    }
}
