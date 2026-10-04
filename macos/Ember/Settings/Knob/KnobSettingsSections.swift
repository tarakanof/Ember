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
                Toggle(isOn: Binding(
                    get: { page.on },
                    set: { on in knob.edit { $0.pages[index].on = on } })) {
                    Text(knobPageTitle(page.id))
                }
                .disabled(page.on && knob.settings.draft.isLastPageOn(page.id))
                .contextMenu {
                    Button("Move Up") { move(index, by: -1) }.disabled(index == 0)
                    Button("Move Down") { move(index, by: 1) }.disabled(index == pages.count - 1)
                }
                .accessibilityActions {
                    if index > 0 { Button("Move Up") { move(index, by: -1) } }
                    if index < pages.count - 1 { Button("Move Down") { move(index, by: 1) } }
                }
            }
            .onMove { from, to in knob.edit { $0.pages.move(fromOffsets: from, toOffset: to) } }
            Picker("Home page", selection: knob.binding(\.home)) {
                ForEach(pages.filter(\.on)) { p in Text(knobPageTitle(p.id)).tag(p.id) }
            }
        } header: {
            Text("Pages")
        } footer: {
            SectionFooter(text: "Turn the knob to move between pages. Drag to reorder, or Control-click a page. The knob goes back to the home page when it wakes.",
                          error: knob.settings.saveError)
        }
    }

    private func move(_ index: Int, by delta: Int) {
        env.knob.edit { s in
            let to = index + delta
            guard s.pages.indices.contains(to) else { return }
            s.pages.swapAt(index, to)
        }
    }
}

func knobPageTitle(_ id: String) -> LocalizedStringKey {
    switch id {
    case "bot": "Bot"
    case "pomodoro": "Pomodoro"
    case "weather": "Weather"
    default: LocalizedStringKey(id.capitalized)
    }
}

struct KnobBehaviorSection: View {
    @Environment(AppEnvironment.self) private var env

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
            StepperRow(title: "Bot gets sleepy after", value: Binding(
                get: { s.bot.sleepyAfterS / 60 },
                set: { m in knob.edit { $0.bot.sleepyAfterS = m * 60 } }),
                range: 0...(KnobSettings.sleepyRange.upperBound / 60)) { m in
                m == 0 ? Text("Never") : Text(verbatim: DurationText.minutes(m))
            }
            StepperRow(title: "Hold demo mood for", value: knob.binding(\.bot.demoHoldS),
                       range: KnobSettings.demoHoldRange, step: 5) { Text("\($0) s") }
        } header: {
            Text("Behavior")
        } footer: {
            SectionFooter(text: "A shorter check interval shows changes sooner and uses a little more power. Long-press the knob to try a mood; it holds for the time set here.",
                          error: knob.settings.saveError)
        }
    }
}
