import SwiftUI
import EmberKit

/// One Dashboard card: a stock `GroupBox` with a symbol title, an optional
/// trailing header (a count, a total), and a fixed height so a row's cards
/// line up and charts fill them.
struct DashboardCard<Content: View, Accessory: View>: View {
    let title: LocalizedStringKey
    let systemImage: String
    var height: CGFloat = DashboardCardHeight.standard
    @ViewBuilder let content: () -> Content
    @ViewBuilder var accessory: () -> Accessory

    var body: some View {
        GroupBox {
            content()
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
                .padding(DashboardCardHeight.padding)
        } label: {
            HStack(alignment: .firstTextBaseline) {
                Label(title, systemImage: systemImage)
                    .font(.headline)
                Spacer(minLength: 8)
                accessory()
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                    .lineLimit(1)
            }
        }
        .frame(height: height)
    }
}

extension DashboardCard where Accessory == EmptyView {
    init(title: LocalizedStringKey, systemImage: String, height: CGFloat = DashboardCardHeight.standard,
         @ViewBuilder content: @escaping () -> Content) {
        self.init(title: title, systemImage: systemImage, height: height, content: content, accessory: { EmptyView() })
    }
}

/// Card heights (§2.2): fixed so rows line up.
enum DashboardCardHeight {
    static let standard: CGFloat = 220
    static let wide: CGFloat = 260
    static let mirror: CGFloat = 200
    static let padding: CGFloat = 12
}
