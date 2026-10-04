import SwiftUI
import EmberKit

/// Whether a device is reporting, as a Hardware page's header shows it.
enum HardwareStatus: Equatable {
    case online
    /// Reporting faster while the page is open (the knob's live mode).
    case live
    case offline(lastSeen: Date?)
}

/// The top of a Hardware page: the device's status, then the range picker.
struct HardwarePageHeader<Trailing: View>: View {
    let status: HardwareStatus?
    let range: HardwareRange
    let setRange: (HardwareRange) -> Void
    var pickerDisabled = false
    var liveHelp: LocalizedStringKey?
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            statusLabel
            Spacer(minLength: 12)
            trailing()
            Picker("Time range", selection: Binding(get: { range }, set: { setRange($0) })) {
                ForEach(HardwareRange.allCases) { r in Text(Self.title(r)).tag(r) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .disabled(pickerDisabled)
        }
    }

    static func title(_ r: HardwareRange) -> LocalizedStringKey {
        switch r {
        case .fifteenMinutes: "15 Minutes"
        case .hour: "1 Hour"
        case .day: "24 Hours"
        }
    }

    @ViewBuilder private var statusLabel: some View {
        switch status {
        case .offline(let seen)?:
            Label {
                if let seen {
                    Text("Offline, last report \(Text(seen, format: .relative(presentation: .named)))",
                         comment: "Hardware page status; the argument is a relative time (\"5 minutes ago\").")
                } else {
                    Text("Offline")
                }
            } icon: { Image(systemName: "wifi.slash") }
            .foregroundStyle(.orange)
            .font(.callout)
        case .live?:
            Label("Live", systemImage: "dot.radiowaves.left.and.right")
                .foregroundStyle(.green)
                .font(.callout)
                .help(liveHelp.map { Text($0) } ?? Text(verbatim: ""))
        case .online?:
            Label("Online", systemImage: "circle.fill")
                .labelStyle(StatusDotLabelStyle())
                .font(.callout)
                .foregroundStyle(.secondary)
        case nil:
            EmptyView()
        }
    }
}

extension HardwarePageHeader where Trailing == EmptyView {
    init(status: HardwareStatus?, range: HardwareRange, setRange: @escaping (HardwareRange) -> Void,
         pickerDisabled: Bool = false, liveHelp: LocalizedStringKey? = nil) {
        self.init(status: status, range: range, setRange: setRange, pickerDisabled: pickerDisabled,
                  liveHelp: liveHelp, trailing: { EmptyView() })
    }
}

private struct StatusDotLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 4) {
            configuration.icon.font(.system(size: 7)).foregroundStyle(.green)
            configuration.title
        }
    }
}

/// A Hardware page's cards: wide ones alone on a row, standard ones in
/// `columns` columns.
struct HardwareCardGrid<ID: Hashable & Sendable, Card: View>: View {
    let cards: [(id: ID, size: CardSize)]
    let columns: Int
    @ViewBuilder let card: (ID) -> Card

    var body: some View {
        VStack(spacing: HardwareMetrics.spacing) {
            ForEach(Array(DashboardLayout.runs(cards, columns: columns).enumerated()), id: \.offset) { _, run in
                switch run {
                case .wide(let id):
                    card(id)
                case .grid(let ids):
                    LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: HardwareMetrics.spacing, alignment: .top),
                                             count: columns),
                              spacing: HardwareMetrics.spacing) {
                        ForEach(ids, id: \.self) { card($0) }
                    }
                }
            }
        }
    }
}

/// A box for a Hardware page's state when it has no cards to show.
struct HardwareStateBox<Content: View>: View {
    @ViewBuilder let content: () -> Content

    var body: some View {
        GroupBox { content().frame(maxWidth: .infinity, minHeight: 220) }
    }
}

/// Settings › Devices › {device} › Hardware: a scrolling page of cards whose
/// column count follows its width.
struct HardwareScrollPage<Content: View>: View {
    @ViewBuilder let content: (_ columns: Int) -> Content

    @State private var columns = 2

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: HardwareMetrics.spacing) {
                content(columns)
            }
            .padding(20)
            .onGeometryChange(for: Int.self) { HardwareMetrics.columns(forWidth: $0.size.width - 40) } action: { columns = $0 }
        }
    }
}

enum HardwareMetrics {
    static let spacing: CGFloat = 16

    /// 2 columns from 520 pt (the Settings detail is about 560 wide), else 1.
    static func columns(forWidth width: Double) -> Int { width >= 520 ? 2 : 1 }
}
