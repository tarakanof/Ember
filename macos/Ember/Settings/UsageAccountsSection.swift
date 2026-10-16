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
                UsageAccountRowView(row: row,
                                    rename: { name in model.draft.rename(row.id, to: name) },
                                    setHidden: { hidden in model.draft.setHidden(row.id, hidden) })
                    .contentShape(Rectangle())
                    .draggable(UsageAccountDrag(id: row.id))
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
            SectionFooter(text: "The knob shows one usage screen per account, in this order. Clear a name to use the default. Drag to reorder, or Control-click an account. Server-wide.",
                          error: model.saveError ?? (model.isLoaded ? nil : model.loadError))
        }
        .disabled(!model.isLoaded)
    }
}

private struct UsageAccountRowView: View {
    let row: UsageAccountRow
    let rename: (String) -> Void
    let setHidden: (Bool) -> Void
    @State private var name = ""
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
                .accessibilityHidden(true)
                .help("Drag to reorder")
        }
        .onAppear { name = row.customName }
        .onChange(of: row.customName) { _, new in if !focused { name = new } }
        .onChange(of: focused) { _, isFocused in if !isFocused { commit() } }
    }

    private func commit() {
        name = row.normalizedName(name)
        rename(name)
    }

    private var detail: some View {
        HStack(spacing: 0) {
            if let plan = row.planLabel { Text(verbatim: plan + " · ") }
            if let seen = row.updatedAt {
                if row.stale {
                    Text("Stale, updated \(Text(seen, format: .relative(presentation: .named)))",
                         comment: "Settings › Agents › Accounts: no usage report for 10 min; the time is relative, e.g. \"2 hours ago\".")
                } else {
                    Text("Updated \(Text(seen, format: .relative(presentation: .named)))",
                         comment: "Settings › Agents › Accounts: the last usage report; the time is relative, e.g. \"5 min ago\".")
                }
            } else {
                Text("No usage reported yet")
            }
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
