import SwiftUI
import EventKit
import EmberKit

struct RemindersTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var preview: PreviewResponse?

    var body: some View {
        @Bindable var watcher = env.reminderWatcher
        Form {
            Section {
                PanelPreview(
                    title: "REMINDER ALARM",
                    caption: "Pops up when a reminder comes due: gold bell + the reminder title (scrolls on the clock).",
                    enabled: watcher.prefs.enabled,
                    frame: preview?.frames.first(where: { $0.card == "reminder" }))
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.black)
                .listRowInsets(EdgeInsets())
            } footer: {
                Text("Not a rotating tile — it interrupts the clock only when a reminder fires. With a native AWTRIX icon the bell is replaced by that animated icon (device only).")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                accessRow(status: watcher.authStatus)
            } header: {
                Text("Apple Reminders")
            } footer: {
                Text("Ember rings the clock when an Apple Reminder with a due time comes due — across all your lists, while the Mac is awake and Ember is running.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                Toggle("Enable", isOn: $watcher.prefs.enabled)
                    .disabled(watcher.authStatus != .fullAccess)
                Toggle("Sound", isOn: $watcher.prefs.sound)
                Toggle("Keep on screen until dismissed", isOn: $watcher.prefs.hold)
                Stepper("Lead time: \(watcher.prefs.leadMinutes) min",
                        value: $watcher.prefs.leadMinutes, in: 0...60)
                Stepper("Popup duration \(watcher.prefs.popupDuration) s",
                        value: $watcher.prefs.popupDuration, in: 1...120)
                    .disabled(watcher.prefs.hold)
            } header: {
                Text("Behaviour")
            } footer: {
                Text(watcher.prefs.hold
                     ? "The alarm takes over the clock until you press its middle button. Popup duration is ignored while this is on."
                     : "The alarm interrupts for the popup duration, then the clock returns to its normal rotation.")
                    .font(.caption).foregroundStyle(.secondary)
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
        // Re-read the status when the tab appears so a change made in System
        // Settings (or another launch's grant) is reflected without a relaunch.
        .task { env.reminderWatcher.refreshAuthorization() }
        // The alarm frame is static (options don't change its pixels), so one
        // fetch per appearance is enough. Failures keep the blank placeholder.
        .task { preview = try? await env.preview.fetchReminderPreview() }
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
