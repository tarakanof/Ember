import SwiftUI
import EmberKit

struct KnobControlsSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let model = env.settings.controls
        if model.isLoaded {
            content(model)
        }
    }

    @ViewBuilder private func content(_ model: ServerConfigModel<ControlsConfig>) -> some View {
        @Bindable var service = env.knobControls
        let cfg = model.draft
        Section {
            Picker("Run on", selection: Binding(
                get: { model.draft.target },
                set: { v in model.draft.target = v })) {
                Text("Off").tag("")
                ForEach(cfg.targetChoices(thisMac: service.macName), id: \.self) { name in
                    macLabel(name, thisMac: service.macName, listening: cfg.macs.first { $0.name == name }?.listening)
                        .tag(name)
                }
            }
            ForEach(ControlGesture.allCases, id: \.self) { gesture in
                ControlActionRow(gesture: gesture, action: Binding(
                    get: { service.mapping[gesture] },
                    set: { v in service.mapping[gesture] = v }))
            }
            if cfg.target == service.macName, service.mapping.needsEventAccess, !service.eventAccess {
                LabeledContent {
                    Button("Allow…") { service.requestEventAccess() }
                } label: {
                    Text("Media keys and scrolling need Accessibility access for Ember.")
                }
            }
        } header: {
            Text("Controls")
        } footer: {
            SectionFooter(text: "Knob presses and turns run these actions on the Mac picked above. The actions are kept on this Mac only; the server just relays which gesture you made.",
                          error: model.saveError)
        }
        .autosaves(model)
        .onAppear { service.refreshEventAccess() }
        .onChange(of: model.applied?.target) { _, _ in Task { await service.refresh() } }
    }

    private func macLabel(_ name: String, thisMac: String, listening: Bool?) -> Text {
        let base = Text(verbatim: name)
        if name == thisMac {
            return Text("\(base) (this Mac)", comment: "Settings › Knob › Controls: the Mac running this app in the target list (\"m4 (this Mac)\").")
        }
        if listening == false {
            return Text("\(base) (offline)", comment: "Settings › Knob › Controls: a Mac that has stopped polling the server (\"m5 (offline)\").")
        }
        return base
    }
}

private struct ControlActionRow: View {
    let gesture: ControlGesture
    @Binding var action: ControlAction

    var body: some View {
        Picker(selection: Binding(get: { action.kind }, set: { action = action.switched(to: $0) })) {
            ForEach(gesture.isTurn ? ControlKind.turnKinds : ControlKind.pressKinds, id: \.self) { kind in
                Text(kindTitle(kind)).tag(kind)
            }
        } label: {
            Text(gestureTitle(gesture))
        }
        switch action.kind {
        case .media:
            Picker("Key", selection: Binding(get: { action.key ?? .playPause }, set: { action.key = $0 })) {
                Text("Play/Pause").tag(MediaKey.playPause)
                Text("Next Track").tag(MediaKey.next)
                Text("Previous Track").tag(MediaKey.previous)
            }
        case .shortcut:
            TextField("Shortcut name", text: Binding(get: { action.name ?? "" }, set: { action.name = $0 }))
        case .applescript:
            TextField("AppleScript", text: Binding(get: { action.script ?? "" }, set: { action.script = $0 }),
                      axis: .vertical)
                .lineLimit(2...6)
                .font(.body.monospaced())
        case .volume:
            Stepper(value: Binding(get: { action.step ?? 2 }, set: { action.step = $0 }), in: 1...20) {
                Text("\(action.step ?? 2)% per step", comment: "Settings › Knob › Controls: volume change per knob detent (\"2% per step\").")
            }
        case .scroll:
            Stepper(value: Binding(get: { action.lines ?? 3 }, set: { action.lines = $0 }), in: 1...10) {
                Text("\(action.lines ?? 3) lines per step", comment: "Settings › Knob › Controls: lines scrolled per knob detent (\"3 lines per step\").")
            }
            Picker("Direction", selection: Binding(get: { action.axis ?? .vertical }, set: { action.axis = $0 })) {
                Text("Up and down").tag(ScrollAxis.vertical)
                Text("Left and right").tag(ScrollAxis.horizontal)
            }
        case .none, .mute:
            EmptyView()
        }
    }

    private func gestureTitle(_ g: ControlGesture) -> LocalizedStringKey {
        switch g {
        case .push: "Press"
        case .doublePush: "Double press"
        case .longPush: "Long press"
        case .turn: "Turn"
        }
    }

    private func kindTitle(_ k: ControlKind) -> LocalizedStringKey {
        switch k {
        case .none: "Nothing"
        case .media: "Media key"
        case .mute: "Mute"
        case .shortcut: "Run Shortcut"
        case .applescript: "Run AppleScript"
        case .volume: "Volume"
        case .scroll: "Scroll"
        }
    }
}
