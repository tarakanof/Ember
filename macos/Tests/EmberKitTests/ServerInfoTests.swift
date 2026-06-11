import Foundation
import Testing
@testable import EmberKit

@Test func serverVersionPrefersSemverWithCommit() throws {
    // A released server reports a semver; show it with the short commit.
    let json = #"{"binary":"ember","version":"0.9.0","revision":"44143ca88be0b79212b2d043bab3b88f3104fdfc","dirty":false,"go_version":"go1.26.4"}"#
    let info = try JSONDecoder().decode(VersionInfo.self, from: Data(json.utf8))
    #expect(info.short == "0.9.0 · 44143ca")
}

@Test func serverVersionFallsBackToCommitForDevOrOldServer() throws {
    // A local "dev" build (and, by the same path, an older server with no
    // version field) falls back to the binary @ commit form.
    let dev = #"{"binary":"ember","version":"dev","revision":"44143ca88be0b79212b2d043bab3b88f3104fdfc"}"#
    #expect(try JSONDecoder().decode(VersionInfo.self, from: Data(dev.utf8)).short == "ember @ 44143ca")

    let old = #"{"binary":"ember","revision":"6ad0339abc","dirty":true}"#
    #expect(try JSONDecoder().decode(VersionInfo.self, from: Data(old.utf8)).short == "ember @ 6ad0339-dirty")
}
