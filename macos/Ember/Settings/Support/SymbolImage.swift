import SwiftUI

enum EmberSymbol {
    static let printer3D = "ember.3d.printer"
    static let custom: Set<String> = [printer3D]
}

struct SymbolImage: View {
    let name: String

    init(_ name: String) { self.name = name }

    var body: some View {
        if EmberSymbol.custom.contains(name) {
            Image(name)
        } else {
            Image(systemName: name)
        }
    }
}
