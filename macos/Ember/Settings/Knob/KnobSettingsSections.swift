import SwiftUI
import EmberKit

extension KnobModel {
    func binding<V>(_ key: WritableKeyPath<KnobSettings, V>) -> Binding<V> {
        Binding(get: { self.settings.draft[keyPath: key] },
                set: { v in self.edit { $0[keyPath: key] = v } })
    }

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
        CollapsibleSection("Display", group: .knobDisplay, contentDisabled: !knob.settings.isLoaded) {
            InfoToggle("Follow Ember brightness", isOn: knob.binding(\.brightness.followEmber), info: .knobFollowBrightness,
                       requirement: .loading)
            PercentSliderRow(title: "Brightness", percent: knob.percent(\.brightness.level))
                .disabled(follow)
            PercentSliderRow(title: "Minimum brightness", percent: knob.percent(\.brightness.floor), range: 1...100,
                             info: .knobMinBrightness, requirement: .loading)
            PercentSliderRow(title: "Brightness at startup", percent: knob.percent(\.brightness.startup),
                             info: .knobStartupBrightness, requirement: .loading)
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                if follow {
                    Text("The knob dims and brightens with the clock. It never goes below the minimum, except during quiet hours.")
                } else {
                    Text("The knob stays at this brightness. It never goes below the minimum, except during quiet hours.")
                }
                SaveErrorFooter(error: knob.settings.saveError)
            }
        }
    }
}

struct KnobQuietSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let knob = env.knob
        if let quiet = knob.settings.draft.quiet {
            Section {
                InfoToggle("Calm bot", isOn: Binding(
                    get: { quiet.calm },
                    set: { v in knob.edit { $0.quiet?.calm = v } }),
                    info: .knobQuietCalm, requirement: .loading)
                PercentSliderRow(title: "Quiet brightness", percent: Binding(
                    get: { DeviceUnits.brightnessPercent(raw: quiet.dimLevel) },
                    set: { p in knob.edit { $0.quiet?.dimLevel = DeviceUnits.brightnessRaw(percent: p) } }),
                    range: 1...100, info: .knobQuietDim, requirement: .loading)
            } header: {
                Text("Quiet Hours")
            } footer: {
                SectionFooter(text: "Applies while quiet hours are on. Set the hours in Sounds & Alerts.",
                              error: knob.settings.saveError)
            }
            .disabled(!knob.settings.isLoaded)
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

struct KnobPageDrag: Codable, Transferable {
    let id: String
    static var transferRepresentation: some TransferRepresentation {
        CodableRepresentation(contentType: .json)
    }
}

@MainActor
func showKnobHardware(_ knobID: String) {
    showSettings(.device(knobID, .hardware(.health)))
}

func knobPageTitle(_ id: String) -> LocalizedStringKey {
    switch id {
    case "bot": "Bot"
    case "pomodoro": "Pomodoro"
    case "weather": "Weather"
    case "nowplaying": "Now Playing"
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

    private var intervalsRequirement: SettingsInfoRequirement {
        env.knob.settings.isLoaded ? .diagnosticsOn : .loading
    }

    var body: some View {
        let knob = env.knob
        let s = knob.settings.draft
        CollapsibleSection("Behavior", group: .knobBehavior, contentDisabled: !knob.settings.isLoaded) {
            DecimalStepperRow(title: "Check Ember every", value: Binding(
                get: { Double(s.pollMS) / 1000 },
                set: { v in knob.edit { $0.pollMS = Int((v * 1000).rounded()) } }),
                range: Double(KnobSettings.pollRange.lowerBound) / 1000...Double(KnobSettings.pollRange.upperBound) / 1000,
                step: 0.5, info: .knobPoll, requirement: .loading) { v in
                Text("\(Text(v, format: .number.precision(.fractionLength(0...1)))) s",
                     comment: "Settings › Knob: how often the knob polls Ember, in seconds (\"2.5 s\").")
            }
            Picker("Diagnostics", selection: knob.binding(\.diagnostics)) {
                Text("Off").tag(KnobDiagnostics.off)
                Text("Basic").tag(KnobDiagnostics.basic)
                Text("Full").tag(KnobDiagnostics.full)
            }
            if s.statsIntervalS != nil, s.liveIntervalS != nil, knob.knob?.supports(feature: KnobCaps.statsIntervals) == true {
                InfoRow("Send stats every", info: .knobStatsInterval, requirement: intervalsRequirement) { label in
                    Picker(selection: knob.binding(\.statsIntervalS)) {
                        ForEach(KnobSettings.choices(KnobSettings.statsIntervals, current: s.statsIntervalS), id: \.self) { sec in
                            Text(verbatim: DurationText.interval(sec))
                                .tag(Int?.some(sec))
                        }
                    } label: { label }
                }
                .disabled(s.diagnostics == .off)
                InfoRow("Live stats every", info: .knobLiveInterval, requirement: intervalsRequirement) { label in
                    Picker(selection: knob.binding(\.liveIntervalS)) {
                        ForEach(KnobSettings.choices(KnobSettings.liveIntervals, current: s.liveIntervalS), id: \.self) { sec in
                            Text(verbatim: DurationText.interval(sec)).tag(Int?.some(sec))
                        }
                    } label: { label }
                }
                .disabled(s.diagnostics == .off)
            }
            if s.display != nil {
                InfoToggle("Fast display link", isOn: Binding(
                    get: { s.display?.fastLink ?? true },
                    set: { v in knob.edit { $0.display = KnobSettings.Display(fastLink: v) } }),
                    info: .knobFastLink, requirement: .loading)
            }
            LabeledContent {
                Button("Show Hardware") { if let id = knob.knob?.id { showKnobHardware(id) } }
                    .disabled(s.diagnostics == .off)
            } label: {
                Text(diagnosticsHelp(s.diagnostics)).foregroundStyle(.secondary).font(.callout)
            }
        } footer: {
            SaveErrorFooter(error: knob.settings.saveError)
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
                range: 0...(KnobSettings.sleepyRange.upperBound / 60), info: .knobSleepy, requirement: .loading) { m in
                m == 0 ? Text("Never") : Text(verbatim: DurationText.minutes(m))
            }
            StepperRow(title: "Hold demo mood for", value: knob.binding(\.bot.demoHoldS),
                       range: KnobSettings.demoHoldRange, step: 5) { Text("\($0) s") }
            if s.bot.sourceLabel != nil {
                InfoToggle("Show the host's name", isOn: Binding(
                    get: { s.bot.sourceLabel ?? true },
                    set: { v in knob.edit { $0.bot.sourceLabel = v } }),
                    info: .knobSourceLabel, requirement: .loading)
            }
            if s.bot.workingRing != nil {
                InfoToggle("Animate the outline while working", isOn: Binding(
                    get: { s.bot.workingRing ?? true },
                    set: { v in knob.edit { $0.bot.workingRing = v } }),
                    info: .knobWorkingRing, requirement: .loading)
            }
        } header: {
            Text("Bot")
        } footer: {
            SectionFooter(text: "Long-press the knob to try a mood; it holds for the time set here.",
                          error: knob.settings.saveError)
        }
    }
}

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
