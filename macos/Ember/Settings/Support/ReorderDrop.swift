import SwiftUI
import UniformTypeIdentifiers
import EmberKit

extension UTType {
    static let emberClockApp = UTType(exportedAs: ReorderDragType.clockApp)
    static let emberKnobPage = UTType(exportedAs: ReorderDragType.knobPage)
    static let emberUsageAccount = UTType(exportedAs: ReorderDragType.usageAccount)
}

private struct ReorderDrop<Item: Transferable>: ViewModifier {
    let action: (Item) -> Void
    @State private var targeted = false

    func body(content: Content) -> some View {
        content
            .background(RoundedRectangle(cornerRadius: 6).fill(Color.accentColor.opacity(targeted ? 0.18 : 0)))
            .dropDestination(for: Item.self) { drops, _ in
                guard let drop = drops.first else { return false }
                action(drop)
                return true
            } isTargeted: { targeted = $0 }
    }
}

extension View {
    func reorderDrop<Item: Transferable>(_ type: Item.Type, perform action: @escaping (Item) -> Void) -> some View {
        modifier(ReorderDrop<Item>(action: action))
    }
}
