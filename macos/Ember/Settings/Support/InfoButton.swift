import SwiftUI
import EmberKit

/// An ⓘ button that trails a Settings row's label (#290): hovering shows the
/// one-line summary, clicking opens a popover with the details. Use it
/// through `InfoRow`, which also gives VoiceOver a named action on the row,
/// since a control's label isn't always reachable on its own.
struct InfoButton: View {
    let info: SettingsInfo
    let title: LocalizedStringKey
    @Binding var isPresented: Bool

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
            Text(info.detail)
                .fixedSize(horizontal: false, vertical: true)
                .frame(width: 280, alignment: .leading)
                .padding(14)
        }
    }
}

/// A row's title with its info button, if it has one.
struct InfoLabel: View {
    let title: LocalizedStringKey
    let info: SettingsInfo?
    @Binding var isPresented: Bool

    var body: some View {
        if let info {
            HStack(spacing: 4) {
                Text(title)
                InfoButton(info: info, title: title, isPresented: $isPresented)
            }
        } else {
            Text(title)
        }
    }
}

/// Builds a row around an `InfoLabel` and owns its popover state:
/// `InfoRow("Bottom bar", info: .bottomBar) { label in Picker(…) { … } label: { label } }`.
struct InfoRow<Row: View>: View {
    private let title: LocalizedStringKey
    private let info: SettingsInfo?
    private let row: (InfoLabel) -> Row
    @State private var shown = false

    init(_ title: LocalizedStringKey, info: SettingsInfo?, @ViewBuilder row: @escaping (InfoLabel) -> Row) {
        self.title = title
        self.info = info
        self.row = row
    }

    var body: some View {
        let label = InfoLabel(title: title, info: info, isPresented: $shown)
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

/// A switch whose label carries an info button.
struct InfoToggle: View {
    let title: LocalizedStringKey
    @Binding var isOn: Bool
    let info: SettingsInfo

    init(_ title: LocalizedStringKey, isOn: Binding<Bool>, info: SettingsInfo) {
        self.title = title
        self._isOn = isOn
        self.info = info
    }

    var body: some View {
        InfoRow(title, info: info) { label in
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
            Toggle("No info here", isOn: $on)
        }
    }
    .formStyle(.grouped)
    .frame(width: 480)
}
