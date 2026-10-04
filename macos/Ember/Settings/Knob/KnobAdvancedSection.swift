import SwiftUI
import EmberKit

struct KnobAdvancedSection: View {
    @Environment(AppEnvironment.self) private var env
    let changeWiFi: () -> Void
    @State private var name = ""
    @State private var confirmRotate = false
    @State private var confirmReset = false
    @FocusState private var nameFocused: Bool

    var body: some View {
        let model = env.knob
        if let knob = model.knob {
            Section {
                LabeledContent("Name") {
                    HStack(spacing: 6) {
                        TextField("Name", text: $name, prompt: Text(verbatim: knob.name))
                            .labelsHidden()
                            .multilineTextAlignment(.trailing)
                            .frame(maxWidth: 220)
                            .focused($nameFocused)
                            .onSubmit(rename)
                        if model.running.contains(.rename) { ProgressView().controlSize(.mini) }
                    }
                }
                LabeledContent("Token") {
                    HStack(spacing: 8) {
                        if knob.rotationPending {
                            Text("New token waiting for the knob").foregroundStyle(.secondary)
                        }
                        Button("Rotate Token…") { confirmRotate = true }
                            .disabled(model.running.contains(.rotate))
                    }
                }
                if model.registeredKnobOnUSB, let port = model.connectedPort {
                    LabeledContent("USB") {
                        HStack(spacing: 8) {
                            Button("Change Wi-Fi…", action: changeWiFi)
                            Button("Factory Reset…", role: .destructive) { confirmReset = true }
                                .disabled(model.running.contains(.factoryReset))
                        }
                    }
                    .confirmationDialog("Erase the knob?", isPresented: $confirmReset, titleVisibility: .visible) {
                        Button("Factory Reset", role: .destructive) {
                            Task { await model.factoryReset(port: port) }
                        }
                        Button("Cancel", role: .cancel) {}
                    } message: {
                        Text("The knob forgets its Wi-Fi, Ember address and token, then restarts to its setup screen. Ember keeps its settings here until you forget it.")
                    }
                }
            } header: {
                Text("Advanced")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Rotating the token is routine hygiene, not a fix for a leak: the knob collects the new token on its next check-in, and so could anyone holding the old one. If the token may have leaked, forget the knob and set it up again over USB.")
                    if !model.registeredKnobOnUSB {
                        Text("Plug the knob into this Mac to change its Wi-Fi or erase it.")
                    }
                    ForEach([KnobAction.rename, .rotate, .factoryReset], id: \.self) { action in
                        if let e = model.actionErrors[action] {
                            Label { Text(e.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                                .foregroundStyle(.red)
                        }
                    }
                }
            }
            .onAppear { name = knob.name }
            .onChange(of: knob.name) { _, new in if !nameFocused { name = new } }
            .onChange(of: nameFocused) { _, focused in if !focused { rename() } }
            .confirmationDialog("Rotate the knob's token?", isPresented: $confirmRotate, titleVisibility: .visible) {
                Button("Rotate Token") { Task { await model.rotate() } }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("The knob picks up the new token on its next check-in, within a minute if it's online. The old token stops working once the knob uses the new one, or after 24 hours.")
            }
        }
    }

    private func rename() {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let knob = env.knob.knob else { return }
        guard !trimmed.isEmpty else { name = knob.name; return }
        guard trimmed != knob.name else { return }
        Task { await env.knob.rename(trimmed) }
    }
}
