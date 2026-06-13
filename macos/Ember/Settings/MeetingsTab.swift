import SwiftUI
import EmberKit

struct MeetingsTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var config = MeetingsConfig()
    @State private var state: MeetingsState?
    @State private var preview: PreviewResponse?
    @State private var save: SaveState = .idle
    @State private var loaded = false
    @State private var writer = DebouncedWriter(delay: .milliseconds(600))
    @State private var lastApplied: MeetingsConfig?

    // Per-section fold state, persisted so the user's arrangement sticks.
    @AppStorage("meetingsFold.tile") private var tileExpanded = false
    @AppStorage("meetingsFold.popup") private var popupExpanded = false
    @AppStorage("meetingsFold.upcoming") private var upcomingExpanded = false

    var body: some View {
        Form {
            Section {
                PanelPreview(
                    title: "NEXT MEETING",
                    caption: "Calendar icon · title · minutes to start. Long titles scroll on the device.",
                    enabled: config.enabled,
                    frame: preview?.frames.first(where: { $0.card == "meeting" }))
                    .padding(.horizontal, 14)
                    .padding(.vertical, 10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.black)
                    .listRowInsets(EdgeInsets())
            } footer: {
                Text("Shows your real next meeting, or a sample when there's no live data. The options below don't change this preview.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section {
                Toggle("Show next meeting", isOn: $config.enabled)
            } footer: {
                VStack(alignment: .leading, spacing: 2) {
                    if config.icsUrlsConfigured == 0 {
                        Label("No calendar feeds configured", systemImage: "exclamationmark.triangle")
                            .font(.caption).foregroundStyle(.orange)
                        Text("Set EMBER_MEETINGS_ICS_URLS on the server (comma-separated secret ICS URLs). They're credentials — they never appear in this app or the server's API.")
                            .font(.caption).foregroundStyle(.secondary)
                    } else {
                        Label("\(config.icsUrlsConfigured) calendar feed(s) configured", systemImage: "checkmark.circle")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    statusCaption
                }
            }

            Section(isExpanded: $tileExpanded) {
                Stepper("Show when next meeting ≤ \(config.tileLeadMinutes) min away",
                        value: $config.tileLeadMinutes, in: 5...480, step: 5)
            } header: {
                Text("Tile")
            }

            // NB: Section(isExpanded:content:header:footer:) doesn't exist, so
            // the chime note lives inside the fold as a caption row.
            Section(isExpanded: $popupExpanded) {
                Stepper(config.popupLeadMinutes == 0
                        ? "Popup: off"
                        : "Popup \(config.popupLeadMinutes) min before",
                        value: $config.popupLeadMinutes, in: 0...60, step: 1)
                Toggle("Chime", isOn: $config.chime)
                    .disabled(config.popupLeadMinutes == 0)
                Text("The chime is muted during quiet hours (set under Pomodoro).")
                    .font(.caption).foregroundStyle(.secondary)
            } header: {
                Text("Popup")
            }

            Section(isExpanded: $upcomingExpanded) {
                if let items = state?.upcoming, !items.isEmpty {
                    ForEach(items, id: \.id) { item in
                        HStack {
                            Text(item.title)
                            Spacer()
                            Text(item.start, style: .relative)
                                .foregroundStyle(.secondary)
                        }
                        .font(.caption)
                    }
                } else {
                    Text("No meetings in the next 36 hours.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                if let fetchedAt = state?.fetchedAt {
                    Text("Updated \(fetchedAt, style: .relative) ago")
                        .font(.caption).foregroundStyle(.secondary)
                }
            } header: {
                Text("Upcoming")
            }
        }
        .formStyle(.grouped)
        .toolbar {
            ToolbarItem { Button("Reload from server") { Task { await load() } } }
        }
        .task {
            if !loaded {
                await load()
                loaded = true
            }
        }
        .onChange(of: config) { _, _ in scheduleSave() }
    }

    @ViewBuilder private var statusCaption: some View {
        switch save {
        case .idle:   EmptyView()
        case .saving: Text("Saving…").font(.caption).foregroundStyle(.secondary)
        case .saved:  Label("Saved", systemImage: "checkmark.circle")
                        .font(.caption).foregroundStyle(.secondary)
        case .error(let m): Label(m, systemImage: "exclamationmark.triangle")
                        .font(.caption).foregroundStyle(.red)
        }
    }

    private func refreshPreview() async {
        if let p = try? await env.preview.fetchMeetingsPreview() {
            preview = p
        }
    }

    private func scheduleSave() {
        guard loaded else { return }
        guard config != lastApplied else { return }
        save = .saving
        let cfg = config
        writer.schedule {
            do {
                try await env.meetings.putConfig(cfg)
                await MainActor.run { lastApplied = cfg; save = .saved }
            } catch let e as APIError where e.isUnauthorized {
                await MainActor.run { save = .error("Unauthorized — check the token in Connection.") }
            } catch {
                await MainActor.run { save = .error("Save failed: \(error.localizedDescription)") }
            }
        }
    }

    private func load() async {
        save = .idle
        do {
            config = try await env.meetings.getConfig()
            lastApplied = config
        } catch {
            save = .error("Couldn't load meetings config (server offline?).")
        }
        await refreshPreview()
        state = try? await env.meetings.state()
    }
}
