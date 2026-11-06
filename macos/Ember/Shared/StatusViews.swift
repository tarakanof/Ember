import SwiftUI
import EmberKit

struct StaleChip: View {
    let since: Date

    var body: some View {
        Label {
            Text("Stale · \(Text(since, style: .relative))")
        } icon: {
            Image(systemName: "exclamationmark.triangle")
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
    }
}

struct PhaseBadge: View {
    let state: Session.State

    var body: some View {
        Label {
            Text(state.displayName)
        } icon: {
            Circle()
                .fill(EmberColors.state(state))
                .frame(width: 8, height: 8)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(state.displayName))
    }
}

struct StatTile: View {
    let title: LocalizedStringKey
    let value: String
    var unit: String? = nil
    var symbol: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                if let symbol {
                    Image(systemName: symbol)
                        .foregroundStyle(.secondary)
                        .accessibilityHidden(true)
                }
                Text(value)
                    .font(.title2.monospacedDigit())
                if let unit {
                    Text(unit)
                        .font(.body)
                        .foregroundStyle(.secondary)
                }
            }
            Text(title)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue([value, unit].compactMap { $0 }.joined(separator: " "))
    }
}
