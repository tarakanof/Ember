import SwiftUI
import EmberKit

struct RemindersTab: View {
    @Environment(AppEnvironment.self) private var env

    @State private var config = RemindersConfig()
    @State private var save: SaveState = .idle
    @State private var loaded = false
    @State private var writer = DebouncedWriter(delay: .milliseconds(600))
    @State private var lastApplied: RemindersConfig?

    var body: some View {
        Form {
            Section {
                Toggle("Enable reminders", isOn: $config.enabled)
                TextField("Timezone", text: $config.timezone, prompt: Text("Europe/Amsterdam"))
                Stepper("Popup duration \(config.popupDurationSeconds) s",
                        value: $config.popupDurationSeconds, in: 1...120, step: 1)
            } footer: {
                Text("Reminders fire as a bell-icon alarm at their local time (in the timezone above). Each fires at most once per day.")
                    .font(.caption).foregroundStyle(.secondary)
            }

            Section("Reminders") {
                if config.items.isEmpty {
                    Text("No reminders yet.").font(.caption).foregroundStyle(.secondary)
                }
                ForEach($config.items) { $item in
                    ReminderRow(item: $item) { remove(item.id) }
                }
                Button {
                    config.items.append(Reminder(id: UUID().uuidString))
                } label: {
                    Label("Add reminder", systemImage: "plus.circle")
                }
            }

            Section {
                Toggle("Use native AWTRIX icon", isOn: $config.useNativeIcon)
                TextField("Native icon ID", text: $config.nativeIconId, prompt: Text("e.g. 1234"))
                    .disabled(!config.useNativeIcon)
            } header: {
                Text("Icon")
            } footer: {
                statusCaption
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Reminders")
        .toolbar {
            ToolbarItem { Button("Reload from server") { Task { await load() } } }
        }
        .task { if !loaded { await load() } }
        .onChange(of: config) { _, _ in scheduleSave() }
    }

    private func remove(_ id: String) {
        config.items.removeAll { $0.id == id }
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

    private func scheduleSave() {
        guard loaded else { return }
        guard config != lastApplied else { return }
        save = .saving
        let cfg = config
        writer.schedule {
            do {
                try await env.reminders.putConfig(cfg)
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
            let cfg = try await env.reminders.getConfig()
            lastApplied = cfg
            var c = cfg
            if c.timezone.isEmpty { c.timezone = TimeZone.current.identifier }
            config = c
            loaded = true
            if c != cfg { scheduleSave() }   // persist the autofilled timezone
        } catch {
            loaded = true
            save = .error("Couldn't load reminders config (server offline?).")
        }
    }
}

/// One editable reminder row: time, text, weekday set, sound, enable + delete.
private struct ReminderRow: View {
    @Binding var item: Reminder
    let onDelete: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                DatePicker("", selection: timeBinding, displayedComponents: .hourAndMinute)
                    .labelsHidden()
                TextField("Message", text: $item.text, prompt: Text("Stand-up"))
                Toggle("", isOn: $item.enabled).labelsHidden()
                Button(role: .destructive, action: onDelete) {
                    Image(systemName: "trash")
                }
                .buttonStyle(.borderless)
            }
            WeekdayChips(days: $item.days)
            Toggle("Sound", isOn: $item.sound)
                .font(.caption)
        }
        .padding(.vertical, 2)
    }

    /// Bridges the "HH:MM" string to a Date for the hour/minute DatePicker.
    private var timeBinding: Binding<Date> {
        Binding(
            get: {
                let parts = item.time.split(separator: ":")
                var c = DateComponents()
                c.hour = parts.count == 2 ? Int(parts[0]) ?? 9 : 9
                c.minute = parts.count == 2 ? Int(parts[1]) ?? 0 : 0
                return Calendar.current.date(from: c) ?? Date()
            },
            set: { date in
                let c = Calendar.current.dateComponents([.hour, .minute], from: date)
                item.time = String(format: "%02d:%02d", c.hour ?? 0, c.minute ?? 0)
            }
        )
    }
}

/// A compact weekday selector (S M T W T F S). Empty selection = every day.
private struct WeekdayChips: View {
    @Binding var days: [Int]
    private let labels = ["S", "M", "T", "W", "T", "F", "S"]

    var body: some View {
        HStack(spacing: 4) {
            ForEach(0..<7, id: \.self) { d in
                let on = days.contains(d)
                Text(labels[d])
                    .font(.caption2.weight(.semibold))
                    .frame(width: 20, height: 20)
                    .background(on ? Color.accentColor : Color.secondary.opacity(0.18),
                                in: Circle())
                    .foregroundStyle(on ? Color.white : Color.secondary)
                    .onTapGesture { toggle(d) }
            }
            if days.isEmpty {
                Text("every day").font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    private func toggle(_ d: Int) {
        if let i = days.firstIndex(of: d) { days.remove(at: i) } else { days.append(d); days.sort() }
    }
}
