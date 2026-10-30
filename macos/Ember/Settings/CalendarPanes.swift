import SwiftUI
import EventKit
import EmberKit

struct CalendarSourcePane: View {
    @Environment(AppEnvironment.self) private var env

    private var model: ServerConfigModel<MeetingsConfig> { env.settings.meetings }
    private var reminders: ServerConfigModel<ReminderPrefs> { env.settings.reminders }

    var body: some View {
        @Bindable var model = model
        @Bindable var reminders = reminders
        let watcher = env.reminderWatcher
        let c = model.draft
        Form {
            LoadStateSection(isLoaded: model.isLoaded, error: model.loadError,
                             offMessage: "This server doesn't support meetings. Update the Ember server.",
                             retry: { await model.load() })

            Section {
                Toggle("Show next meeting", isOn: $model.draft.enabled)
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

            LoadStateSection(isLoaded: reminders.isLoaded, error: reminders.loadError,
                             offMessage: "This server doesn't keep reminder settings. Update the Ember server.",
                             retry: { await reminders.load() })

            Section {
                remindersAccess(watcher.authStatus)
                Toggle("Ring the clock for due reminders", isOn: $reminders.draft.enabled)
                    .disabled(watcher.authStatus != .fullAccess || !reminders.isLoaded)
                StepperRow(title: "Ring", value: $reminders.draft.leadMinutes,
                           range: ReminderPrefs.leadRange.including(reminders.draft.leadMinutes)) { m in
                    m == 0 ? Text("When due") : Text("\(m) min early")
                }
                .disabled(!reminders.isLoaded || !reminders.draft.enabled)
            } header: {
                Text("Apple Reminders")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Saved on the server. Any Mac running Ember with Reminders access can ring the clock while it's awake; each reminder rings once.")
                    SaveErrorFooter(error: reminders.saveError)
                    if let error = watcher.lastFireError {
                        Label("Last reminder didn't reach the clock: \(error)", systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }
            }

            if reminders.draft.enabled, !watcher.upcoming.isEmpty {
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

            ShownOnSection(source: .calendar)
        }
        .formStyle(.grouped)
        .autosaves(model)
        .autosaves(reminders)
        .reloads {
            env.reminderWatcher.refreshAuthorization()
            let s = env.settings
            async let meetings: Void = s.meetings.load()
            async let rems: Void = s.reminders.load()
            _ = await (meetings, rems)
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

struct ClockCalendarAppPane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var meetingPreview = PreviewModel()
    @State private var reminderPreview = PreviewModel()

    private var model: ClockAppLens<ClockConfig.Calendar> { env.clockCalendar }
    private var reminders: ServerConfigModel<ReminderPrefs> { env.settings.reminders }

    var body: some View {
        let model = model
        let draft = model.binding
        @Bindable var reminders = reminders
        let r = reminders.draft
        let c = model.draft
        Form {
            SourceLinkSection(source: .calendar,
                              isOff: model.isLoaded && !c.enabled && reminders.isLoaded && !r.enabled)

            Section {
                VStack(alignment: .leading, spacing: 14) {
                    PanelPreview(title: "Next meeting", caption: "Calendar icon, title and minutes to go.",
                                 enabled: c.enabled, frame: meetingPreview.frame("meeting"))
                    PanelPreview(title: "Reminder", caption: "Bell and the reminder's title when it comes due.",
                                 enabled: r.enabled,
                                 frame: reminderPreview.frame("reminder"))
                }
                .settingsPreviewRow()
            }

            LoadStateSection(isLoaded: model.isLoaded, error: model.loadError,
                             offMessage: "This server doesn't support meetings. Update the Ember server.",
                             retry: { await model.load() })

            Section {
                StepperRow(title: "Show tile from", value: draft.tileLeadMinutes,
                           range: (5...240).including(c.tileLeadMinutes), step: 5) { Text("\($0) min before") }
                StepperRow(title: "Popup", value: draft.popupLeadMinutes,
                           range: (0...30).including(c.popupLeadMinutes), info: .meetingPopupLead,
                           requirement: model.isLoaded ? .meetingsOn : .loading) { m in
                    m == 0 ? Text("Off") : Text("\(m) min before")
                }
            } header: {
                Text("Meetings")
            } footer: {
                SaveErrorFooter(error: model.saveError)
            }
            .disabled(!model.isLoaded || !c.enabled)

            Section {
                Toggle("Keep on screen until dismissed", isOn: $reminders.draft.hold)
                StepperRow(title: "Show for", value: $reminders.draft.popupDuration,
                           range: (5...120).including(r.popupDuration), step: 5) { Text("\($0) s") }
                    .disabled(r.hold)
                InfoToggle("Native icon", isOn: $reminders.draft.useNativeIcon, info: .reminderNativeIcon, requirement: .remindersOn)
                TextField("Icon ID", text: $reminders.draft.nativeIconId, prompt: Text(verbatim: "1234"))
                    .disabled(!r.useNativeIcon)
            } header: {
                Text("Apple Reminders")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text(r.hold
                         ? "The reminder stays on the clock until you press its middle button."
                         : "The reminder shows for the time above, then the clock carries on.")
                    Text("Saved on the server and shared by every Mac running Ember.")
                    SaveErrorFooter(error: reminders.saveError)
                }
            }
            .disabled(!reminders.isLoaded || !r.enabled)
        }
        .formStyle(.grouped)
        .autosaves(model)
        .autosaves(reminders)
        .previews(into: meetingPreview) { try await env.preview.fetchMeetingsPreview() }
        .previews(into: reminderPreview) { try await env.preview.fetchReminderPreview() }
        .reloads {
            async let clock: Void = model.load()
            async let rems: Void = env.settings.reminders.load()
            _ = await (clock, rems)
        }
    }
}
