import SwiftUI
import EmberKit

extension KnobModel {
    /// A binding that edits through `edit`, so every change stays valid.
    func binding<V>(_ key: WritableKeyPath<KnobSettings, V>) -> Binding<V> {
        Binding(get: { self.settings.draft[keyPath: key] },
                set: { v in self.edit { $0[keyPath: key] = v } })
    }

    /// A 0–255 level shown as a percentage.
    func percent(_ key: WritableKeyPath<KnobSettings, Int>) -> Binding<Int> {
        Binding(get: { DeviceUnits.brightnessPercent(raw: self.settings.draft[keyPath: key]) },
                set: { p in self.edit { $0[keyPath: key] = DeviceUnits.brightnessRaw(percent: p) } })
    }
}

struct KnobDisplaySection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let knob = env.knob
        let follow = knob.settings.draft.brightness.followEmber
        Section {
            Toggle("Follow Ember brightness", isOn: knob.binding(\.brightness.followEmber))
            PercentSliderRow(title: "Brightness", percent: knob.percent(\.brightness.level))
                .disabled(follow)
            PercentSliderRow(title: "Minimum brightness", percent: knob.percent(\.brightness.floor), range: 1...100)
            PercentSliderRow(title: "Brightness at startup", percent: knob.percent(\.brightness.startup))
        } header: {
            Text("Display")
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                if follow {
                    Text("The knob dims and brightens with the clock. It never goes below the minimum.")
                } else {
                    Text("The knob stays at this brightness. It never goes below the minimum.")
                }
                SaveErrorFooter(error: knob.settings.saveError)
            }
        }
    }
}

struct KnobPagesSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let knob = env.knob
        let pages = knob.settings.draft.pages
        Section {
            ForEach(Array(pages.enumerated()), id: \.element.id) { index, page in
                HStack {
                    Toggle(isOn: Binding(
                        get: { page.on },
                        set: { on in knob.edit { $0.pages[index].on = on } })) {
                        Text(knobPageTitle(page.id))
                    }
                    .disabled(page.on && knob.settings.draft.isLastPageOn(page.id))
                    Image(systemName: "line.3.horizontal")
                        .foregroundStyle(.tertiary)
                        .accessibilityHidden(true)
                        .help("Drag to reorder")
                }
                .contentShape(Rectangle())
                .draggable(KnobPageDrag(id: page.id))
                .dropDestination(for: KnobPageDrag.self) { drops, _ in
                    guard let drop = drops.first else { return false }
                    knob.edit { $0.movePage(drop.id, to: page.id) }
                    return true
                }
                .contextMenu {
                    Button("Move Up") { move(page.id, by: -1) }.disabled(index == 0)
                    Button("Move Down") { move(page.id, by: 1) }.disabled(index == pages.count - 1)
                }
                .accessibilityActions {
                    if index > 0 { Button("Move Up") { move(page.id, by: -1) } }
                    if index < pages.count - 1 { Button("Move Down") { move(page.id, by: 1) } }
                }
            }
            Picker("Home page", selection: knob.binding(\.home)) {
                ForEach(pages.filter(\.on)) { p in Text(knobPageTitle(p.id)).tag(p.id) }
            }
        } header: {
            Text("Pages")
        } footer: {
            SectionFooter(text: "Hold the knob down and turn it to change pages. Drag to reorder, or Control-click a page. The knob goes back to the home page when it wakes.",
                          error: knob.settings.saveError)
        }
    }

    private func move(_ id: String, by delta: Int) {
        env.knob.edit { $0.movePage(id, by: delta) }
    }
}

/// A page row being dragged to a new place in the knob's order.
struct KnobPageDrag: Codable, Transferable {
    let id: String
    static var transferRepresentation: some TransferRepresentation {
        CodableRepresentation(contentType: .json)
    }
}

/// Selects the knob's Hardware page in Settings.
@MainActor
func showKnobHardware(_ knobID: String) {
    showSettings(.device(knobID, .hardware(.health)))
}

func knobPageTitle(_ id: String) -> LocalizedStringKey {
    switch id {
    case "bot": "Bot"
    case "pomodoro": "Pomodoro"
    case "weather": "Weather"
    default: LocalizedStringKey(id.capitalized)
    }
}

struct KnobPollSection: View {
    @Environment(AppEnvironment.self) private var env

    private func diagnosticsHelp(_ level: KnobDiagnostics) -> LocalizedStringKey {
        switch level {
        case .off: "The knob sends no hardware stats."
        case .basic: "Processor, memory, temperature and Wi-Fi, every minute."
        case .full: "Adds network requests and rendering. Uses a little more of the knob's time."
        }
    }

    var body: some View {
        let knob = env.knob
        let s = knob.settings.draft
        Section {
            DecimalStepperRow(title: "Check Ember every", value: Binding(
                get: { Double(s.pollMS) / 1000 },
                set: { v in knob.edit { $0.pollMS = Int((v * 1000).rounded()) } }),
                range: Double(KnobSettings.pollRange.lowerBound) / 1000...Double(KnobSettings.pollRange.upperBound) / 1000,
                step: 0.5) { v in
                Text("\(Text(v, format: .number.precision(.fractionLength(0...1)))) s",
                     comment: "Settings › Knob: how often the knob polls Ember, in seconds (\"2.5 s\").")
            }
            Picker("Diagnostics", selection: knob.binding(\.diagnostics)) {
                Text("Off").tag(KnobDiagnostics.off)
                Text("Basic").tag(KnobDiagnostics.basic)
                Text("Full").tag(KnobDiagnostics.full)
            }
            if s.statsIntervalS != nil, s.liveIntervalS != nil, knob.knob?.supportsStatsIntervals == true {
                Picker("Send stats every", selection: knob.binding(\.statsIntervalS)) {
                    ForEach(KnobSettings.choices(KnobSettings.statsIntervals, current: s.statsIntervalS), id: \.self) { sec in
                        Text(verbatim: DurationText.interval(sec))
                            .tag(Int?.some(sec))
                    }
                }
                .disabled(s.diagnostics == .off)
                Picker("Live stats every", selection: knob.binding(\.liveIntervalS)) {
                    ForEach(KnobSettings.choices(KnobSettings.liveIntervals, current: s.liveIntervalS), id: \.self) { sec in
                        Text(verbatim: DurationText.interval(sec)).tag(Int?.some(sec))
                    }
                }
                .disabled(s.diagnostics == .off)
            }
            if s.display != nil {
                Toggle("Fast display link", isOn: Binding(
                    get: { s.display?.fastLink ?? true },
                    set: { v in knob.edit { $0.display = KnobSettings.Display(fastLink: v) } }))
                    .help("Runs the knob's screen link at 80 MHz instead of 40: smoother motion, less tearing. The knob restarts to apply it and goes back to 40 MHz by itself if the link fails a check.")
            }
            LabeledContent {
                Button("Show Hardware") { if let id = knob.knob?.id { showKnobHardware(id) } }
                    .disabled(s.diagnostics == .off)
            } label: {
                Text(diagnosticsHelp(s.diagnostics)).foregroundStyle(.secondary).font(.callout)
            }
        } header: {
            Text("Behavior")
        } footer: {
            SectionFooter(text: s.statsIntervalS == nil || knob.knob?.supportsStatsIntervals != true
                          ? "A shorter check interval shows changes sooner and uses a little more power."
                          : "A shorter check interval shows changes sooner and uses a little more power. Live stats are sent only while the knob's stats are open in Ember. Shorter stats intervals mean more requests from the knob and more memory on the server.",
                          error: knob.settings.saveError)
        }
    }
}

struct KnobBotSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let knob = env.knob
        let s = knob.settings.draft
        Section {
            StepperRow(title: "Bot gets sleepy after", value: Binding(
                get: { s.bot.sleepyAfterS / 60 },
                set: { m in knob.edit { $0.bot.sleepyAfterS = m * 60 } }),
                range: 0...(KnobSettings.sleepyRange.upperBound / 60)) { m in
                m == 0 ? Text("Never") : Text(verbatim: DurationText.minutes(m))
            }
            StepperRow(title: "Hold demo mood for", value: knob.binding(\.bot.demoHoldS),
                       range: KnobSettings.demoHoldRange, step: 5) { Text("\($0) s") }
            if s.bot.sourceLabel != nil {
                Toggle("Show the host's name", isOn: Binding(
                    get: { s.bot.sourceLabel ?? true },
                    set: { v in knob.edit { $0.bot.sourceLabel = v } }))
            }
            if s.bot.workingRing != nil {
                Toggle("Animate the outline while working", isOn: Binding(
                    get: { s.bot.workingRing ?? true },
                    set: { v in knob.edit { $0.bot.workingRing = v } }))
            }
        } header: {
            Text("Bot")
        } footer: {
            SectionFooter(text: s.bot.sourceLabel == nil
                          ? "Long-press the knob to try a mood; it holds for the time set here."
                          : "Long-press the knob to try a mood; it holds for the time set here. The name of the computer whose agent is working or waiting shows along the bottom of the face.",
                          error: knob.settings.saveError)
        }
    }
}

/// An app's page on the knob: on or off, and where it sits in the order.
struct KnobPageSection: View {
    @Environment(AppEnvironment.self) private var env
    let app: AppID

    var body: some View {
        let knob = env.knob
        let pages = knob.settings.draft.pages
        if let id = AppCatalog.knobPage(app), let index = pages.firstIndex(where: { $0.id == id }) {
            let page = pages[index]
            Section {
                Toggle("Show on the knob", isOn: Binding(
                    get: { page.on },
                    set: { on in knob.edit { $0.pages[index].on = on } }))
                    .disabled(page.on && knob.settings.draft.isLastPageOn(page.id))
                LabeledContent("Position") {
                    Text("Page \(index + 1) of \(pages.count)",
                         comment: "Settings › Knob › Apps: where an app's page sits in the knob's page order (\"Page 2 of 3\").")
                }
                if knob.settings.draft.home == id {
                    LabeledContent("Home page") { Image(systemName: "checkmark").accessibilityLabel(Text("Yes")) }
                }
            } header: {
                Text("Page")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 4) {
                        Text("Order and home page are set in Pages.")
                        Button("Edit Pages…") { showSettings(.device(knob.knob?.id ?? DeviceKind.knob.placeholderID, .apps)) }
                            .buttonStyle(.link)
                    }
                    SaveErrorFooter(error: knob.settings.saveError)
                }
            }
        }
    }
}
