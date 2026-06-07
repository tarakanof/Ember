import SwiftUI
import EventKit
import EmberKit

struct RemindersTab: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        @Bindable var watcher = env.reminderWatcher
        Form {
            Section {
                accessRow(status: watcher.authStatus)
            } header: {
                Text("Apple Reminders")
            } footer: {
                Text("Ember rings the clock when an Apple Reminder with a due time comes due — across all your lists, while the Mac is awake and Ember is running.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Behaviour") {
                Toggle("Enable", isOn: $watcher.prefs.enabled)
                    .disabled(watcher.authStatus != .fullAccess)
                Toggle("Sound", isOn: $watcher.prefs.sound)
                Stepper("Lead time: \(watcher.prefs.leadMinutes) min",
                        value: $watcher.prefs.leadMinutes, in: 0...60)
                Stepper("Popup duration \(watcher.prefs.popupDuration) s",
                        value: $watcher.prefs.popupDuration, in: 1...120)
            }

            Section("Icon") {
                Toggle("Use native AWTRIX icon", isOn: $watcher.prefs.useNativeIcon)
                TextField("Native icon ID", text: $watcher.prefs.nativeIconId, prompt: Text("e.g. 1234"))
                    .disabled(!watcher.prefs.useNativeIcon)
            }

            if !watcher.upcoming.isEmpty {
                Section("Next due") {
                    ForEach(watcher.upcoming) { r in
                        HStack {
                            Text(r.title.isEmpty ? "(untitled)" : r.title)
                            Spacer()
                            Text(r.due, format: .dateTime.weekday().hour().minute())
                                .foregroundStyle(.secondary)
                        }
                        .font(.caption)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Reminders")
        // Re-read the status when the tab appears so a change made in System
        // Settings (or another launch's grant) is reflected without a relaunch.
        .task { env.reminderWatcher.refreshAuthorization() }
    }

    @ViewBuilder private func accessRow(status: EKAuthorizationStatus) -> some View {
        switch status {
        case .fullAccess:
            Label("Reminders access granted", systemImage: "checkmark.circle.fill")
                .font(.caption).foregroundStyle(.green)
        case .denied, .restricted:
            VStack(alignment: .leading, spacing: 4) {
                Label("Reminders access denied", systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.red)
                Text("Allow Reminders for Ember in System Settings → Privacy & Security → Reminders, then reopen this tab.")
                    .font(.caption2).foregroundStyle(.secondary)
                Button("Open System Settings…") {
                    if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Reminders") {
                        NSWorkspace.shared.open(url)
                    }
                }
            }
        default:
            VStack(alignment: .leading, spacing: 4) {
                Label("Reminders access not granted", systemImage: "circle.dashed")
                    .font(.caption).foregroundStyle(.secondary)
                Button("Grant access to Reminders") {
                    Task { _ = await env.reminderWatcher.requestAccess() }
                }
            }
        }
    }
}
