import SwiftUI
import EventKit
import EmberKit

/// Calendar alerts on the clock: meetings from the server's ICS feeds, and
/// Apple Reminders due on this Mac. Their chimes are under Sounds & Alerts.
struct CalendarPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var meetingPreview: PreviewResponse?
    @State private var reminderPreview: PreviewResponse?

    private var model: ServerConfigModel<MeetingsConfig> { env.settings.meetings }

    var body: some View {
        @Bindable var model = model
        @Bindable var watcher = env.reminderWatcher
        let c = model.draft
        Form {
            Section {
                VStack(alignment: .leading, spacing: 14) {
                    PanelPreview(title: "Next meeting", caption: "Calendar icon, title and minutes to go.",
                                 enabled: c.enabled, frame: meetingPreview?.frames.first { $0.card == "meeting" })
                    PanelPreview(title: "Reminder", caption: "Bell and the reminder's title when it comes due.",
                                 enabled: watcher.prefs.enabled,
                                 frame: reminderPreview?.frames.first { $0.card == "reminder" })
                }
                .settingsPreviewRow()
            }

            LoadStateSection(isLoaded: model.isLoaded, error: model.loadError,
                             offMessage: "This server doesn't support meetings. Update the Ember server.",
                             retry: { await model.load() })

            Section {
                Toggle("Show next meeting", isOn: $model.draft.enabled)
                Group {
                    StepperRow(title: "Show tile from", value: $model.draft.tileLeadMinutes,
                               range: (5...240).including(c.tileLeadMinutes), step: 5) { Text("\($0) min before") }
                    StepperRow(title: "Popup", value: $model.draft.popupLeadMinutes,
                               range: (0...30).including(c.popupLeadMinutes)) { m in
                        m == 0 ? Text("Off") : Text("\(m) min before")
                    }
                }
                .disabled(!c.enabled)
            } header: {
                Text("Meetings")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    if model.isLoaded {
                        if c.icsUrlsConfigured == 0 {
                            Label("No calendar feeds. Set EMBER_MEETINGS_ICS_URLS on the server; the URLs are secrets and never leave it.",
                                  systemImage: "exclamationmark.triangle.fill")
                                .foregroundStyle(.orange)
                        } else {
                            Text("^[\(c.icsUrlsConfigured) calendar feed](inflect: true) on the server.")
                        }
                    }
                    SaveErrorFooter(error: model.saveError)
                }
            }
            .disabled(!model.isLoaded)

            Section {
                if let items = env.live.meetings.value?.upcoming, !items.isEmpty {
                    ForEach(items, id: \.id) { item in
                        LabeledContent {
                            Text(item.start, format: .dateTime.weekday(.abbreviated).hour().minute())
                        } label: {
                            Text(verbatim: item.title)
                        }
                    }
                } else {
                    Text("No meetings in the next 36 hours.").foregroundStyle(.secondary)
                }
            } header: {
                Text("Upcoming")
            } footer: {
                if let at = env.live.meetings.value?.fetchedAt {
                    Text("Updated \(at, format: .relative(presentation: .named)).")
                }
            }

            Section {
                remindersAccess(watcher.authStatus)
                Toggle("Ring the clock for due reminders", isOn: $watcher.prefs.enabled)
                    .disabled(watcher.authStatus != .fullAccess)
                Group {
                    StepperRow(title: "Ring", value: $watcher.prefs.leadMinutes,
                               range: (0...30).including(watcher.prefs.leadMinutes)) { m in
                        m == 0 ? Text("When due") : Text("\(m) min early")
                    }
                    Toggle("Keep on screen until dismissed", isOn: $watcher.prefs.hold)
                    StepperRow(title: "Show for", value: $watcher.prefs.popupDuration,
                               range: (5...120).including(watcher.prefs.popupDuration), step: 5) { Text("\($0) s") }
                        .disabled(watcher.prefs.hold)
                    Toggle("Native icon", isOn: $watcher.prefs.useNativeIcon)
                    TextField("Icon ID", text: $watcher.prefs.nativeIconId, prompt: Text(verbatim: "1234"))
                        .disabled(!watcher.prefs.useNativeIcon)
                }
                .disabled(!watcher.prefs.enabled)
            } header: {
                Text("Apple Reminders")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text(watcher.prefs.hold
                         ? "The reminder stays on the clock until you press its middle button."
                         : "The reminder shows for the time above, then the clock carries on.")
                    Text("These settings apply only to this Mac, while it's awake and Ember is running.")
                    if let error = watcher.lastFireError {
                        Label("Last reminder didn't reach the clock: \(error)", systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }
            }

            if watcher.prefs.enabled, !watcher.upcoming.isEmpty {
                Section("Next Due") {
                    ForEach(watcher.upcoming) { r in
                        LabeledContent {
                            Text(r.due, format: .dateTime.weekday(.abbreviated).hour().minute())
                        } label: {
                            if r.title.isEmpty { Text("Untitled") } else { Text(verbatim: r.title) }
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .autosaves(model)
        .reloads {
            env.reminderWatcher.refreshAuthorization()
            await model.load()
            meetingPreview = (try? await env.preview.fetchMeetingsPreview()) ?? meetingPreview
            reminderPreview = (try? await env.preview.fetchReminderPreview()) ?? reminderPreview
        }
    }

    @ViewBuilder
    private func remindersAccess(_ status: EKAuthorizationStatus) -> some View {
        switch status {
        case .fullAccess:
            Label("Reminders access on", systemImage: "checkmark.circle.fill").foregroundStyle(.secondary)
        case .denied, .restricted:
            LabeledContent {
                Button("Open Privacy Settings…") {
                    openSystemSettings("x-apple.systempreferences:com.apple.preference.security?Privacy_Reminders")
                }
            } label: {
                Label("Reminders access off", systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
            }
        default:
            LabeledContent {
                Button("Allow Access…") { Task { _ = await env.reminderWatcher.requestAccess() } }
            } label: {
                Label("Ember needs access to Reminders", systemImage: "checklist")
            }
        }
    }
}
