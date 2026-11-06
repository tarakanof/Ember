import SwiftUI
import EmberKit

struct CollapsibleSection<Content: View, Footer: View>: View {
    let title: LocalizedStringKey
    let group: SettingsGroup
    var summary: Text?
    var contentDisabled = false
    @ViewBuilder let content: () -> Content
    @ViewBuilder let footer: () -> Footer
    @AppStorage(SettingsGroup.storageKey) private var collapsedGroups = ""

    init(_ title: LocalizedStringKey, group: SettingsGroup, summary: Text? = nil, contentDisabled: Bool = false,
         @ViewBuilder content: @escaping () -> Content,
         @ViewBuilder footer: @escaping () -> Footer = { EmptyView() }) {
        self.title = title
        self.group = group
        self.summary = summary
        self.contentDisabled = contentDisabled
        self.content = content
        self.footer = footer
    }

    private var expanded: Bool { !group.isCollapsed(in: collapsedGroups) }

    var body: some View {
        Section {
            if expanded { content().disabled(contentDisabled) }
        } header: {
            Button {
                withAnimation(.snappy(duration: 0.2)) {
                    collapsedGroups = group.setCollapsed(expanded, in: collapsedGroups)
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "chevron.right")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        .accessibilityHidden(true)
                    Text(title)
                        .font(.headline)
                        .foregroundStyle(.primary)
                    Spacer()
                    if !expanded, let summary {
                        summary
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                .padding(.bottom, expanded ? 0 : 12)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityValue(expanded ? Text("Expanded") : Text("Collapsed"))
            .accessibilityHint(expanded ? Text("Collapses this group") : Text("Expands this group"))
        } footer: {
            if expanded { footer() }
        }
    }
}
