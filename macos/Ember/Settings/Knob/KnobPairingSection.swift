import SwiftUI
import EmberKit

struct KnobPairingSection: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let pairing = env.knob.pairing
        if pairing.isVisible {
            Section {
                ForEach(pairing.notices) { notice in
                    KnobPairingNoticeRow(notice: notice)
                }
                ForEach(pairing.claims) { claim in
                    KnobClaimRow(claim: claim)
                }
            } header: {
                Text("Pairing request")
            } footer: {
                if !pairing.claims.isEmpty {
                    Text("Type the 6-digit code the knob shows on its screen. Only approve a request you started.")
                }
            }
        }
    }
}

private struct KnobPairingNoticeRow: View {
    @Environment(AppEnvironment.self) private var env
    let notice: KnobPairingNotice

    var body: some View {
        HStack {
            Label {
                switch notice.failure {
                case .alreadyPaired:
                    Text("Knob \(notice.knob) already paired.",
                         comment: "Settings › Knob pairing notice; the argument is the knob's short ID (\"61FC8C\").")
                case .gone:
                    Text("The request from knob \(notice.knob) expired. It will show a new code.",
                         comment: "Settings › Knob pairing notice; the argument is the knob's short ID (\"61FC8C\").")
                case .wrongCode:
                    Text("The request from knob \(notice.knob) is closed after too many wrong codes. It will show a new code.",
                         comment: "Settings › Knob pairing notice; the argument is the knob's short ID (\"61FC8C\").")
                case .alreadyApproved:
                    Text("The request from knob \(notice.knob) was already approved.",
                         comment: "Settings › Knob pairing notice; the argument is the knob's short ID (\"61FC8C\").")
                case .other(let error):
                    Text("Couldn't pair: \(Text(error.message))",
                         comment: "Settings › Knob pairing request error; the argument is a short reason (\"Server unreachable\").")
                }
            } icon: {
                Image(systemName: "exclamationmark.triangle.fill")
            }
            .foregroundStyle(.orange)
            Spacer()
            Button("Dismiss") { env.knob.pairing.dismiss(notice) }
        }
    }
}

private struct KnobClaimRow: View {
    @Environment(AppEnvironment.self) private var env
    let claim: KnobClaim
    @State private var code = ""
    @State private var confirmReplace = false

    private var model: KnobModel { env.knob }
    private var replaces: KnobDevice? { model.pairing.replaces(claim, current: model.knob) }
    private var busy: Bool { model.pairing.running.contains(claim.id) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Knob \(claim.shortID) wants to pair",
                         comment: "Settings › Knob pairing request; the argument is the knob's short ID (\"61FC8C\").")
                        .font(.headline)
                    HStack(spacing: 4) {
                        Text(verbatim: claim.ip)
                        if !claim.fw.isEmpty {
                            Text(verbatim: "·")
                            Text("fw \(claim.fw)", comment: "Settings › Knob pairing request; the argument is the firmware version (\"0.9.110\").")
                        }
                        Text(verbatim: "·")
                        Text(claim.createdAt, format: .relative(presentation: .named))
                    }
                    .font(.callout)
                    .foregroundStyle(.secondary)
                }
            } icon: {
                Image(systemName: "dial.medium")
            }
            HStack {
                Text("Code")
                TextField("Code", text: $code, prompt: Text("6 digits"))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .monospaced()
                    .frame(width: 110)
                    .onSubmit(approve)
                    .disabled(busy)
                Button("Approve", action: approve)
                    .disabled(busy || !KnobPairingCode.isValid(code))
                Button("Decline", role: .destructive) { Task { await model.pairing.decline(claim) } }
                    .disabled(busy)
                if busy { ProgressView().controlSize(.small) }
            }
            if claim.knownDeviceID != nil {
                Text("Settings are kept.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            if let replaces {
                Label {
                    Text("Approving replaces “\(replaces.name)”. Ember forgets that knob afterwards.",
                         comment: "Settings › Knob pairing request warning; the argument is the name of the knob already set up.")
                } icon: {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                }
                .font(.callout)
            }
            if let failure = model.pairing.failures[claim.id] {
                failureLabel(failure)
            }
        }
        .padding(.vertical, 4)
        .confirmationDialog(Text("Replace “\(replaces?.name ?? "")”?",
                                 comment: "Knob setup confirm title; the argument is the name of the knob already set up (\"Desk knob\")."),
                            isPresented: $confirmReplace, titleVisibility: .visible) {
            Button("Replace") { send() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Ember shows one knob. After this pairing it forgets the other knob and revokes its token.")
        }
    }

    private func approve() {
        guard KnobPairingCode.isValid(code), !busy else { return }
        if replaces != nil { confirmReplace = true } else { send() }
    }

    private func send() {
        Task {
            if await model.approve(claim, code: code) { code = "" }
        }
    }

    @ViewBuilder private func failureLabel(_ failure: KnobPairingFailure) -> some View {
        Label {
            switch failure {
            case .wrongCode(let left) where left > 0:
                Text("That code is wrong. \(left) tries left.",
                     comment: "Settings › Knob pairing request; the argument is how many wrong codes the server still accepts for this request.")
            case .other(let error):
                Text("Couldn't pair: \(Text(error.message))",
                     comment: "Settings › Knob pairing request error; the argument is a short reason (\"Server unreachable\").")
            case .wrongCode, .gone, .alreadyApproved, .alreadyPaired:
                EmptyView()
            }
        } icon: {
            Image(systemName: "exclamationmark.triangle.fill")
        }
        .font(.callout)
        .foregroundStyle(.red)
    }
}
