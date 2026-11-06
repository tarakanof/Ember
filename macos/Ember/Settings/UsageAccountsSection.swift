import SwiftUI
import UniformTypeIdentifiers
import EmberKit

struct UsageAccountsSection: View {
    let model: ServerConfigModel<UsageAccounts>

    var body: some View {
        let rows = model.draft.rows
        Section {
            if model.isLoaded, rows.isEmpty {
                Text("No accounts yet. Claude Code and Codex add theirs when they report usage.")
                    .foregroundStyle(.secondary)
            }
            ForEach(Array(rows.enumerated()), id: \.element.id) { index, row in
                UsageAccountRowView(row: row, saveStatus: model.status,
                                    rename: { name in
                                        model.draft.rename(row.id, to: name)
                                        model.scheduleSave()
                                    },
                                    setHidden: { hidden in model.draft.setHidden(row.id, hidden) })
                    .reorderDrop(UsageAccountDrag.self) { drop in model.draft.move(drop.id, to: row.id) }
                    .contextMenu {
                        Button("Move Up") { model.draft.move(row.id, by: -1) }.disabled(index == 0)
                        Button("Move Down") { model.draft.move(row.id, by: 1) }.disabled(index == rows.count - 1)
                    }
                    .accessibilityActions {
                        if index > 0 { Button("Move Up") { model.draft.move(row.id, by: -1) } }
                        if index < rows.count - 1 { Button("Move Down") { model.draft.move(row.id, by: 1) } }
                    }
            }
        } header: {
            Text("Accounts")
        } footer: {
            SectionFooter(text: "The knob shows one usage screen per account, in this order. Clear a name to use the default. Drag a row by its handle to reorder, or Control-click it. Server-wide.",
                          error: model.saveError ?? (model.isLoaded ? nil : model.loadError))
        }
        .disabled(!model.isLoaded)
    }
}

private struct UsageAccountRowView: View {
    let row: UsageAccountRow
    let saveStatus: SaveState
    let rename: (String) -> Void
    let setHidden: (Bool) -> Void
    @State private var name = ""
    @State private var committed: String?
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: 10) {
            UsageAccountMark(tool: row.tool)
                .foregroundStyle(row.hidden ? .tertiary : .secondary)
            VStack(alignment: .leading, spacing: 2) {
                TextField("Name", text: $name, prompt: Text(verbatim: row.defaultName))
                    .labelsHidden()
                    .textFieldStyle(.plain)
                    .foregroundStyle(row.hidden ? .secondary : .primary)
                    .focused($focused)
                    .onSubmit(commit)
                    .accessibilityLabel(Text("Name of \(row.displayName)",
                                             comment: "Settings › Agents › Accounts: accessibility label of an account's name field; the name is the account's."))
                detail
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 8)
            Toggle(isOn: Binding(get: { !row.hidden }, set: { setHidden(!$0) })) {
                Text("Show \(row.displayName) on the knob",
                     comment: "Settings › Agents › Accounts: accessibility label of an account's switch; the name is the account's.")
            }
            .labelsHidden()
            .toggleStyle(.switch)
            .controlSize(.small)
            .help("Show on the knob")
            Image(systemName: "line.3.horizontal")
                .foregroundStyle(.tertiary)
                .frame(width: 20, height: 20)
                .contentShape(Rectangle())
                .draggable(UsageAccountDrag(id: row.id))
                .accessibilityHidden(true)
                .help("Drag to reorder")
        }
        .onAppear { name = row.customName }
        .onChange(of: row.customName) { _, new in if !focused { name = new } }
        .onChange(of: saveStatus) { _, new in
            if case .error = new, name == committed { name = row.customName }
        }
        .onChange(of: focused) { _, isFocused in if !isFocused { commit() } }
        .onDisappear(perform: commit)
    }

    private func commit() {
        name = row.normalizedName(name)
        committed = name
        rename(name)
    }

    private var detail: Text {
        let plan = row.planLabel
        guard let seen = row.updatedAt else {
            return plan.map {
                Text("\($0) · No usage reported yet",
                     comment: "Settings › Agents › Accounts: the plan as the server and knob name it (\"Max\", \"Pro\"; not translated), and no usage report yet.")
            } ?? Text("No usage reported yet")
        }
        let when = Text(seen, format: .relative(presentation: .named))
        switch (plan, row.stale) {
        case (let plan?, true):
            return Text("\(plan) · Stale, updated \(when)",
                        comment: "Settings › Agents › Accounts: the plan as the server and knob name it (\"Max\"; not translated), then no usage report for 10 min; the time is relative (\"2 hours ago\").")
        case (let plan?, false):
            return Text("\(plan) · Updated \(when)",
                        comment: "Settings › Agents › Accounts: the plan as the server and knob name it (\"Max\"; not translated), then the last usage report; the time is relative (\"5 min ago\").")
        case (nil, true):
            return Text("Stale, updated \(when)",
                        comment: "Settings › Agents › Accounts: no usage report for 10 min; the time is relative, e.g. \"2 hours ago\".")
        case (nil, false):
            return Text("Updated \(when)",
                        comment: "Settings › Agents › Accounts: the last usage report; the time is relative, e.g. \"5 min ago\".")
        }
    }
}

private struct UsageAccountMark: View {
    let tool: String

    var body: some View {
        Group {
            if let cg = AgentMark.image(tool) {
                Image(decorative: cg, scale: 1)
                    .resizable()
                    .renderingMode(.template)
                    .interpolation(.high)
                    .aspectRatio(contentMode: .fit)
                    .frame(height: 14)
            } else {
                Image(systemName: "person.crop.circle")
            }
        }
        .frame(width: 22, height: 16)
        .accessibilityLabel(Text(AppNames.display(tool)))
    }
}

struct UsageAccountDrag: Codable, Transferable {
    let id: String
    static var transferRepresentation: some TransferRepresentation {
        CodableRepresentation(contentType: .emberUsageAccount)
    }
}
