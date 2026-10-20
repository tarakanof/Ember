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
            let rotations = knob.knob?.rotationChoices(current: knob.settings.draft.display?.rotation) ?? []
            if !rotations.isEmpty {
                InfoRow("Rotation", info: .knobRotation, requirement: .loading) { label in
                    Picker(selection: Binding(
                        get: { knob.settings.draft.display?.rotation },
                        set: { v in knob.edit { $0.display?.rotation = v } })) {
                        ForEach(rotations, id: \.self) { choice in
                            rotationLabel(choice).tag(Int?.some(choice.degrees))
                        }
                    } label: { label }
                }
            }
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                if follow {
                    Text("The knob dims and brightens with the clock. It never goes below the minimum, except during quiet hours. Set the hours in Sounds & Alerts.")
                } else {
                    Text("The knob stays at this brightness. It never goes below the minimum, except during quiet hours. Set the hours in Sounds & Alerts.")
                }
                if knob.knob?.supports(feature: KnobCaps.idleDim) == false {
                    Text("Idle dimming needs newer knob firmware.")
                }
                SaveErrorFooter(error: knob.settings.saveError)
            }
        }
    }
}

private func rotationLabel(_ choice: KnobRotationChoice) -> Text {
    let degrees = Text(verbatim: "\(choice.degrees)°")
    guard !choice.supported else { return degrees }
    return Text("\(degrees) (not supported by this firmware)",
                comment: "Settings › Knob › Display: a saved rotation the knob's firmware can't apply (\"180° (not supported by this firmware)\").")
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

struct KnobIdleSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let knob = env.knob
        if knob.knob?.supports(feature: KnobCaps.idleDim) == true {
            let idle = knob.settings.draft.idle
            let dim = idle?.dimAfterS ?? 0
            let off = idle?.offAfterS ?? 0
            Section {
                Picker("Dim after", selection: Binding(
                    get: { dim },
                    set: { v in knob.edit { $0.editIdle { $0.dimAfterS = v } } })) {
                    ForEach(KnobSettings.idleDimAfterChoices(off: off, current: dim), id: \.self) { sec in
                        sec == 0 ? Text("Never").tag(sec) : Text(verbatim: DurationText.interval(sec)).tag(sec)
                    }
                }
                PercentSliderRow(title: "Dim level", percent: Binding(
                    get: { DeviceUnits.brightnessPercent(raw: idle?.dimLevel ?? KnobSettings.idleDimDefault) },
                    set: { p in knob.edit { $0.editIdle { $0.dimLevel = DeviceUnits.brightnessRaw(percent: p) } } }),
                    range: 1...100)
                    .disabled(dim == 0)
                Picker("Turn off after", selection: Binding(
                    get: { off },
                    set: { v in knob.edit { $0.editIdle { $0.offAfterS = v } } })) {
                    ForEach(KnobSettings.idleOffAfterChoices(dim: dim, current: off), id: \.self) { sec in
                        sec == 0 ? Text("Never").tag(sec) : Text(verbatim: DurationText.interval(sec)).tag(sec)
                    }
                }
            } header: {
                Text("Idle")
            } footer: {
                SectionFooter(text: "Turning the knob or touching it wakes it.", error: knob.settings.saveError)
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
                        VStack(alignment: .leading, spacing: 2) {
                            Text(knobPageTitle(page.id))
                            if page.on, knob.knob?.supports(page: page.id) == false {
                                Text("Not supported by this knob's firmware",
                                     comment: "Settings › Knob › Pages: a page that is on but the knob's firmware can't show.")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                    .disabled(page.on && knob.settings.draft.isLastPageOn(page.id))
                    Image(systemName: "line.3.horizontal")
                        .foregroundStyle(.tertiary)
                        .accessibilityHidden(true)
                        .help("Drag to reorder")
                }
                .contentShape(Rectangle())
                .draggable(KnobPageDrag(id: page.id))
                .reorderDrop(KnobPageDrag.self) { drop in
                    knob.edit { $0.movePage(drop.id, to: page.id) }
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
            if knob.settings.draft.showsSwipeToggle(for: knob.knob) {
                Toggle("Swipe up or down to change pages", isOn: Binding(
                    get: { knob.settings.draft.swipePages ?? true },
                    set: { v in knob.edit { $0.swipePages = v } }))
            }
        } header: {
            Text("Pages")
        } footer: {
            if knob.settings.draft.showsSwipeFooter(for: knob.knob) {
                SectionFooter(text: "Hold the knob down and turn it, or swipe up or down, to change pages. Drag to reorder, or Control-click a page. The knob goes back to the home page when it wakes.",
                              error: knob.settings.saveError)
            } else {
                SectionFooter(text: "Hold the knob down and turn it to change pages. Drag to reorder, or Control-click a page. The knob goes back to the home page when it wakes.",
                              error: knob.settings.saveError)
            }
        }
    }

    private func move(_ id: String, by delta: Int) {
        env.knob.edit { $0.movePage(id, by: delta) }
    }
}

struct KnobPageDrag: Codable, Transferable {
    let id: String
    static var transferRepresentation: some TransferRepresentation {
        CodableRepresentation(contentType: .emberKnobPage)
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
                    set: { v in knob.edit { $0.display?.fastLink = v } }),
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
        let moments = knob.knob?.supports(feature: KnobCaps.wxMoments) == true
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
            if moments, s.bot.weatherMoments != nil {
                InfoToggle("Weather moments", isOn: Binding(
                    get: { s.bot.weatherMoments ?? true },
                    set: { v in knob.edit { $0.bot.weatherMoments = v } }),
                    info: .knobWeatherMoments, requirement: .loading)
            }
            if moments, s.bot.weatherEveryMin != nil {
                let every = KnobSettings.weatherEveryShown(s.bot.weatherEveryMin ?? KnobSettings.weatherEveryFloor)
                StepperRow(title: "Show the weather every", value: Binding(
                    get: { every },
                    set: { m in knob.edit { $0.bot.weatherEveryMin = KnobSettings.weatherEveryStepped(from: every, to: m) } }),
                    range: KnobSettings.weatherEveryRange, step: KnobSettings.weatherEveryStep,
                    info: .knobWeatherEvery, requirement: knob.settings.isLoaded ? .weatherMomentsOn : .loading) { m in
                    m == 0 ? Text("Never") : Text(verbatim: DurationText.minutes(m))
                }
                .disabled(s.bot.weatherMoments == false)
            }
        } header: {
            Text("Bot")
        } footer: {
            SectionFooter(text: "Long-press the knob to try a mood; it holds for the time set here.",
                          error: knob.settings.saveError)
        }
    }
}

struct KnobAppOptionsSection: View {
    @Environment(AppEnvironment.self) private var env
    let app: AppID

    var body: some View {
        let knob = env.knob
        switch app {
        case .focus where knob.knob?.supports(feature: KnobCaps.pomoColors) == true:
            focus(knob)
        case .weather where knob.knob?.supports(feature: KnobCaps.wxFaceList) == true:
            weather(knob)
        case .nowplaying where knob.knob?.supports(feature: KnobCaps.npDim) == true:
            nowPlaying(knob)
        default:
            EmptyView()
        }
    }

    private func focus(_ knob: KnobModel) -> some View {
        let p = knob.settings.draft.apps?.pomodoro
        let theme = KnobTheme.standard.pomodoro.colors
        return Section {
            InheritableColorRow(title: "Focus colour", hex: Binding(
                get: { p?.focusColor },
                set: { v in knob.edit { $0.editPomodoro { $0.focusColor = v } } }),
                inherited: theme.focus.hex, supportsInherit: true, inheritLabel: "Same as clock")
            InheritableColorRow(title: "Break colour", hex: Binding(
                get: { p?.breakColor },
                set: { v in knob.edit { $0.editPomodoro { $0.breakColor = v } } }),
                inherited: theme.break.hex, supportsInherit: true, inheritLabel: "Same as clock")
        } header: {
            Text("Colours")
        } footer: {
            SectionFooter(text: "Same as clock uses the knob's own colours.", error: knob.settings.saveError)
        }
    }

    private func weather(_ knob: KnobModel) -> some View {
        let rows = knob.settings.draft.weatherFaceRows
        let lastOn = rows.filter { $0.on }.count == 1
        return Section {
            ForEach(rows, id: \.id) { row in
                HStack {
                    Toggle(isOn: Binding(
                        get: { row.on },
                        set: { on in knob.edit { $0.setWeatherFace(row.id, on: on) } })) {
                        knobWeatherFaceTitle(row.id)
                    }
                    .disabled(row.on && lastOn)
                    Button { knob.edit { $0.moveWeatherFace(row.id, by: -1) } } label: {
                        Image(systemName: "chevron.up")
                    }
                    .disabled(!knob.settings.draft.canMoveWeatherFace(row.id, by: -1))
                    .accessibilityLabel(Text("Move Up"))
                    Button { knob.edit { $0.moveWeatherFace(row.id, by: 1) } } label: {
                        Image(systemName: "chevron.down")
                    }
                    .disabled(!knob.settings.draft.canMoveWeatherFace(row.id, by: 1))
                    .accessibilityLabel(Text("Move Down"))
                }
                .buttonStyle(.borderless)
            }
        } header: {
            Text("Faces")
        } footer: {
            SectionFooter(text: "Turning the knob steps through the faces that are on, in this order. At least one stays on.",
                          error: knob.settings.saveError)
        }
    }

    private func nowPlaying(_ knob: KnobModel) -> some View {
        let current = knob.settings.draft.apps?.nowplaying?.dimAfterS ?? KnobSettings.nowPlayingDimDefault
        return Section {
            Picker("Dim after", selection: Binding(
                get: { current },
                set: { v in knob.edit { $0.setNowPlayingDim(v) } })) {
                ForEach(KnobSettings.choices(KnobSettings.nowPlayingDimChoices, current: current), id: \.self) { sec in
                    Text(verbatim: DurationText.interval(sec)).tag(sec)
                }
            }
        } header: {
            Text("Dimming")
        } footer: {
            SectionFooter(text: "How long the knob waits before it dims the Now Playing page.", error: knob.settings.saveError)
        }
    }
}

func knobWeatherFaceTitle(_ id: String) -> Text {
    switch id {
    case "temp": Text("Temperature")
    case "rain": Text("Rain now")
    case "cast": Text("Rain forecast")
    case "wind": Text("Wind")
    case "days": Text("Next days")
    case "sun": Text("Sun")
    case "air": Text("Air")
    default: Text(verbatim: id.capitalized)
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
                    if let note = knobNeedsNewerFirmware(app, knob: knob.knob) {
                        Text(note)
                    }
                    SaveErrorFooter(error: knob.settings.saveError)
                }
            }
        }
    }
}

func knobNeedsNewerFirmware(_ app: AppID, knob: KnobDevice?) -> LocalizedStringKey? {
    guard let knob else { return nil }
    switch app {
    case .focus where !knob.supports(feature: KnobCaps.pomoColors): return "Focus colours need newer knob firmware."
    case .weather where !knob.supports(feature: KnobCaps.wxFaceList): return "Choosing the weather faces needs newer knob firmware."
    case .nowplaying where !knob.supports(feature: KnobCaps.npDim): return "The dim delay needs newer knob firmware."
    default: return nil
    }
}
