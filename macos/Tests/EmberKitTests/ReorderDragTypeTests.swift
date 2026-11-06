import Testing
import Foundation
@testable import EmberKit

@Test func reorderDragTypesAreExportedByTheApp() throws {
    var root = URL(fileURLWithPath: #filePath)
    for _ in 0..<3 { root.deleteLastPathComponent() }
    let data = try Data(contentsOf: root.appendingPathComponent("Ember/Info.plist"))
    let plist = try #require(try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any])
    let declared = try #require(plist["UTExportedTypeDeclarations"] as? [[String: Any]])
    for id in [ReorderDragType.clockApp, ReorderDragType.knobPage, ReorderDragType.usageAccount] {
        let decl = try #require(declared.first { $0["UTTypeIdentifier"] as? String == id }, "\(id) is not exported")
        #expect((decl["UTTypeConformsTo"] as? [String])?.contains("public.data") == true)
        #expect(id.hasPrefix("com.ember.Ember."))
    }
    #expect(Set([ReorderDragType.clockApp, ReorderDragType.knobPage, ReorderDragType.usageAccount]).count == 3)
}
