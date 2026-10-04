import SwiftUI
import AppKit
import EmberKit

struct PermissionsPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var producers: ProducerInstallModel?

    private var model: PermissionsModel { env.permissions }

    var body: some View {
        Form {
            Section {
                ForEach(model.rows) { row in
                    PermissionRowView(row: row, perform: perform)
                }
            } footer: {
                VStack(alignment: .leading, spacing: 6) {
                    Text("macOS gives Local Network access to one build of an app, so a copy of Ember you build yourself can need approval again after every rebuild. If Ember is already on in Local Network settings but still blocked, turn it off and on again.")
                    if let failure = producers?.failure {
                        Label(failure, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                    }
                }
            }

            Section {
                HStack {
                    Button("Check Again") { Task { await model.refresh() } }
                        .disabled(model.isChecking)
                    if model.isChecking {
                        ProgressView().controlSize(.small)
                            .accessibilityLabel("Checking permissions")
                    } else if let at = model.checkedAt {
                        Text("Checked \(at, style: .time)").foregroundStyle(.secondary)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .task {
            if producers == nil { producers = ProducerInstallModel(service: env.producers) }
            await model.refresh(ifOlderThan: PermissionsModel.activationInterval)
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await model.refresh(ifOlderThan: PermissionsModel.activationInterval) }
        }
    }

    private func perform(_ action: PermissionAction) {
        switch action {
        case .openSystemSettings(let pane):
            NSWorkspace.shared.open(pane.url)
        case .requestAccess:
            Task {
                _ = await env.reminderWatcher.requestAccess()
                await model.refresh()
            }
        case .repair:
            Task {
                await producers?.repair()
                await model.refresh()
            }
        case .openPane(let pane):
            showSettings(pane)
        }
    }
}

private struct PermissionRowView: View {
    @Environment(\.settingsTree) private var tree
    let row: PermissionRow
    let perform: (PermissionAction) -> Void

    var body: some View {
        LabeledContent {
            HStack(spacing: 10) {
                PermissionBadge(status: row.status)
                if let action = row.action, row.status != .checking {
                    Button { perform(action) } label: { buttonTitle(action) }
                        .help(Text(buttonHelp(action)))
                }
            }
        } label: {
            Text(title)
            Text(detail)
        }
        .accessibilityElement(children: .contain)
    }

    private var title: LocalizedStringKey {
        switch row.id {
        case .localNetwork: "Local Network"
        case .helperLocalNetwork: "Local Network for Agent Reporting"
        case .backgroundItems: "Background Items"
        case .reminders: "Reminders"
        case .location: "Location"
        }
    }

    private var detail: LocalizedStringKey {
        switch row.id {
        case .localNetwork:
            return "Reaches the server and the clock on your network, and finds them with Bonjour."
        case .helperLocalNetwork where !row.blockedHelpers.isEmpty:
            let helpers = ListFormatter.localizedString(byJoining: row.blockedHelpers.map(\.binaryName))
            return "macOS is blocking \(helpers) from the local network, so its reports don't reach the server."
        case .helperLocalNetwork:
            return "The Claude and Codex helpers send session status to the server. macOS asks for each helper separately."
        case .backgroundItems:
            return "The helpers run as background items, so reporting keeps going after you quit Ember."
        case .reminders:
            return "Reads reminders with a due time to ring the clock. Needed while reminder alarms are on."
        case .location:
            return "Only used when you click Detect in Weather to fill in your coordinates."
        }
    }

    private func buttonTitle(_ action: PermissionAction) -> Text {
        switch action {
        case .openSystemSettings(.localNetwork): Text("Open Local Network Settings…")
        case .openSystemSettings(.reminders): Text("Open Reminders Settings…")
        case .openSystemSettings(.location): Text("Open Location Settings…")
        case .openSystemSettings(.loginItems): Text("Open Login Items…")
        case .requestAccess: Text("Allow Access…")
        case .repair: Text("Repair")
        case .openPane(let pane): Text("Show \(Text(tree.title(for: pane)))", comment: "Button that opens a Settings pane, e.g. Show Weather")
        }
    }

    private func buttonHelp(_ action: PermissionAction) -> LocalizedStringKey {
        switch action {
        case .openSystemSettings: "Opens System Settings, where you can turn it on for Ember."
        case .requestAccess: "Shows the macOS prompt."
        case .repair: "Registers the background helper with macOS again so it starts reporting."
        case .openPane: "Opens the pane that uses it."
        }
    }
}

struct PermissionBadge: View {
    let status: PermissionStatus

    var body: some View {
        if status == .checking {
            HStack(spacing: 4) {
                ProgressView().controlSize(.small)
                Text("Checking…").foregroundStyle(.secondary)
            }
        } else {
            Label { Text(text) } icon: { Image(systemName: symbol) }
                .foregroundStyle(color)
                .accessibilityLabel(Text("Status: \(Text(text))"))
        }
    }

    private var text: LocalizedStringKey {
        switch status {
        case .checking: "Checking…"
        case .granted: "On"
        case .denied: "Off"
        case .notDetermined: "Not asked yet"
        case .needsApproval: "Needs approval"
        case .notRunning: "Not running"
        case .unknown: "Couldn't check"
        case .notInUse: "Not in use"
        }
    }

    private var symbol: String {
        switch status {
        case .granted: "checkmark.circle.fill"
        case .denied: "xmark.circle.fill"
        case .needsApproval, .notRunning: "exclamationmark.triangle.fill"
        case .notDetermined, .unknown, .checking: "questionmark.circle"
        case .notInUse: "minus.circle"
        }
    }

    private var color: Color {
        switch status {
        case .granted: .green
        case .denied: .red
        case .needsApproval, .notRunning, .notDetermined: .orange
        case .unknown, .notInUse, .checking: .secondary
        }
    }
}

struct PermissionsWarning: View {
    let rows: [PermissionRow]

    var body: some View {
        if !rows.isEmpty {
            LabeledContent {
                Button("Review Permissions…") { showSettings(.app(.permissions)) }
            } label: {
                Label { Text(message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                    .foregroundStyle(.orange)
            }
        }
    }

    private var message: LocalizedStringKey {
        if rows.count == 1, rows[0].id == .localNetwork {
            return "Local Network access is off, so Ember can't reach the server."
        }
        if rows.count == 1 { return "A permission Ember needs is off." }
        return "\(rows.count) permissions Ember needs are off."
    }
}
