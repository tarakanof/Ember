import SwiftUI
import EmberKit

struct InfoButton: View {
    let info: SettingsInfo
    let title: LocalizedStringKey
    var requirement: SettingsInfoRequirement?
    @Binding var isPresented: Bool
    @Environment(\.isEnabled) private var rowEnabled

    var body: some View {
        Button { isPresented.toggle() } label: {
            Image(systemName: "info.circle")
        }
        .buttonStyle(.borderless)
        .foregroundStyle(.secondary)
        .help(Text(info.summary))
        .accessibilityLabel(Text("More info about \(Text(title))",
                                 comment: "VoiceOver label of a Settings row's info button; the row's title (\"Fast display link\")."))
        .accessibilityHint(Text(info.summary))
        .popover(isPresented: $isPresented, arrowEdge: .bottom) {
            VStack(alignment: .leading, spacing: 8) {
                let paragraphs = info.popover(rowEnabled: rowEnabled, requirement: requirement)
                ForEach(paragraphs.indices, id: \.self) { i in
                    Text(paragraphs[i])
                        .foregroundStyle(i == 0 ? .primary : .secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(width: 280, alignment: .leading)
            .padding(14)
        }
        .staysEnabled()
    }
}

struct InfoLabel: View {
    let title: LocalizedStringKey
    let info: SettingsInfo?
    var requirement: SettingsInfoRequirement?
    @Binding var isPresented: Bool

    var body: some View {
        if let info {
            HStack(spacing: 4) {
                Text(title)
                InfoButton(info: info, title: title, requirement: requirement, isPresented: $isPresented)
            }
        } else {
            Text(title)
        }
    }
}

struct InfoRow<Row: View>: View {
    private let title: LocalizedStringKey
    private let info: SettingsInfo?
    private let requirement: SettingsInfoRequirement?
    private let row: (InfoLabel) -> Row
    @State private var shown = false

    init(_ title: LocalizedStringKey, info: SettingsInfo?, requirement: SettingsInfoRequirement? = nil,
         @ViewBuilder row: @escaping (InfoLabel) -> Row) {
        self.title = title
        self.info = info
        self.requirement = requirement
        self.row = row
    }

    var body: some View {
        let label = InfoLabel(title: title, info: info, requirement: requirement, isPresented: $shown)
        if info != nil {
            row(label)
                .accessibilityAction(named: Text("More info about \(Text(title))",
                                                 comment: "VoiceOver label of a Settings row's info button; the row's title (\"Fast display link\").")) {
                    shown = true
                }
        } else {
            row(label)
        }
    }
}

struct InfoToggle: View {
    let title: LocalizedStringKey
    @Binding var isOn: Bool
    let info: SettingsInfo
    let requirement: SettingsInfoRequirement?

    init(_ title: LocalizedStringKey, isOn: Binding<Bool>, info: SettingsInfo,
         requirement: SettingsInfoRequirement? = nil) {
        self.title = title
        self._isOn = isOn
        self.info = info
        self.requirement = requirement
    }

    var body: some View {
        InfoRow(title, info: info, requirement: requirement) { label in
            Toggle(isOn: $isOn) { label }
        }
    }
}

#Preview("Info rows") {
    @Previewable @State var on = true
    @Previewable @State var mode = 0
    Form {
        Section {
            InfoToggle("Fast display link", isOn: $on, info: .knobFastLink)
            InfoRow("Bottom bar", info: .bottomBar) { label in
                Picker(selection: $mode) {
                    Text("Session pixels").tag(0)
                    Text("Off").tag(1)
                } label: { label }
            }
            InfoToggle("Disabled row", isOn: $on, info: .knobStatsInterval, requirement: .diagnosticsOn)
                .disabled(true)
            Toggle("No info here", isOn: $on)
        }
    }
    .formStyle(.grouped)
    .frame(width: 480)
}
