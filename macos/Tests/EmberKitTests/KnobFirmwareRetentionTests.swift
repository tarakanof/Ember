import Foundation
import Testing
@testable import EmberKit

private func image(_ version: String, _ channel: String = KnobFirmwareImage.release, build: String? = nil,
                   elf: Bool = true) -> KnobFirmwareImage {
    KnobFirmwareImage(build: build ?? String(version.filter(\.isNumber).suffix(8)), channel: channel, elf: elf,
                      idfVer: "v5.5.5", sha256: "x", size: 1, uploadedAt: .now, version: version)
}

private func knob(on fw: String, build: String? = nil, target: String? = nil, phase: KnobOTAPhase = .idle,
                  available: String? = nil, version: String? = nil) -> KnobOTAStatus {
    KnobOTAStatus(target: target, phase: phase, running: .init(fw: fw, build: build, rollback: true),
                  available: available, version: version)
}

private func versions(_ images: [KnobFirmwareImage]) -> [String] { images.map(\.version) }

private let releases = ["0.9.39", "0.9.40", "0.9.41", "0.9.42", "0.9.43"].map { image($0) }

@Test(arguments: [
    ("0.9.9", "0.9.10", ComparisonResult.orderedAscending),
    ("0.10.0", "0.9.99", .orderedDescending),
    ("1.0.0", "1.0.0", .orderedSame),
    ("0.9.15-rc1", "0.9.15", .orderedAscending),
    ("0.9.15-rc.2", "0.9.15-rc.10", .orderedAscending),
    ("0.9.15-1", "0.9.15-rc", .orderedAscending),
    ("0.9.15-rc", "0.9.15-rc.1", .orderedAscending),
])
func firmwareVersionsCompareSemantically(a: String, b: String, want: ComparisonResult) {
    #expect(KnobFirmwareVersion.compare(a, b) == want)
    #expect(KnobFirmwareVersion.compare(b, a).rawValue == -want.rawValue)
}

@Test func firmwareVersionOrderMatchesTheServersTable() {
    let ordered = ["0.9.2", "0.9.13", "0.9.14-1", "0.9.14-alpha", "0.9.14-alpha.1", "0.9.14-beta", "0.9.14", "0.10.0", "1.0.0"]
    for (i, a) in ordered.enumerated() {
        for (j, b) in ordered.enumerated() {
            let want: ComparisonResult = i < j ? .orderedAscending : (i > j ? .orderedDescending : .orderedSame)
            #expect(KnobFirmwareVersion.compare(a, b) == want, "\(a) vs \(b)")
        }
    }
}

@Test(arguments: [
    ("0.9", "0.9.0", ComparisonResult.orderedSame),
    ("1.2.3-", "1.2.3", .orderedSame),
    ("1.2.3-1", "1.2.3-+1", .orderedAscending),
    ("1.2.3-+1", "1.2.3-+2", .orderedAscending),
])
func firmwareVersionInvalidInputMatchesGo(a: String, b: String, want: ComparisonResult) {
    #expect(KnobFirmwareVersion.compare(a, b) == want)
}

@Test func oldBuildsKeepTheKnobsBuildWhenItIsAlsoTheLatest() {
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.43")))
            == ["0.9.39", "0.9.40", "0.9.41", "0.9.42"])
}

@Test func oldBuildsKeepTheKnobsBuildAndTheLatestRelease() {
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.42")))
            == ["0.9.39", "0.9.40", "0.9.41"])
}

@Test func oldBuildsKeepTheKnobsVersionEvenFromAnotherBuild() {
    let images = [image("0.9.41", build: "aaaa0001"), image("0.9.42", build: "bbbb0002")]
    #expect(versions(KnobFirmwareImage.oldBuilds(images, status: knob(on: "0.9.41", build: "aaaa0001"))).isEmpty)
    #expect(versions(KnobFirmwareImage.oldBuilds(images, status: knob(on: "0.9.41", build: "cccc0003"))).isEmpty)
}

@Test func oldBuildsKeepTheVersionEveryKnobRuns() {
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.43"), otherKnobs: ["0.9.40", "0.9.41"]))
            == ["0.9.39", "0.9.42"])
}

@Test func oldBuildsKeepANewerTestBuildOnly() {
    let newer = releases + [image("0.9.44-rc1", KnobFirmwareImage.test), image("0.9.42-rc1", KnobFirmwareImage.test)]
    #expect(versions(KnobFirmwareImage.oldBuilds(newer, status: knob(on: "0.9.43")))
            == ["0.9.39", "0.9.40", "0.9.41", "0.9.42", "0.9.42-rc1"])
    let older = [image("0.9.10"), image("0.9.9", KnobFirmwareImage.test), image("0.9.8", KnobFirmwareImage.test)]
    #expect(versions(KnobFirmwareImage.oldBuilds(older, status: knob(on: "0.9.10"))) == ["0.9.9", "0.9.8"])
    let testsOnly = [image("0.9.9", KnobFirmwareImage.test), image("0.9.10", KnobFirmwareImage.test)]
    #expect(versions(KnobFirmwareImage.oldBuilds(testsOnly, status: knob(on: "0.9.1"))) == ["0.9.9"])
}

@Test func oldBuildsKeepAnUpdateTarget() {
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.39", target: "0.9.41", phase: .downloading, version: "0.9.41")))
            == ["0.9.40", "0.9.42"])
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.39", target: "0.9.40")))
            == ["0.9.41", "0.9.42"])
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.39", phase: .offered, version: "0.9.41")))
            == ["0.9.40", "0.9.42"])
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: knob(on: "0.9.39", available: "0.9.42")))
            == ["0.9.40", "0.9.41"])
}

@Test func oldBuildsCanDropAFailedBuild() {
    let status = knob(on: "0.9.39", phase: .failed, available: "0.9.41", version: "0.9.41")
    #expect(versions(KnobFirmwareImage.oldBuilds(releases, status: status)) == ["0.9.40", "0.9.41", "0.9.42"])
}

@Test func oldBuildsAreEmptyWhenNothingQualifiesOrTheKnobIsUnknown() {
    #expect(KnobFirmwareImage.oldBuilds([], status: knob(on: "0.9.43")).isEmpty)
    #expect(KnobFirmwareImage.oldBuilds([image("0.9.43")], status: knob(on: "0.9.43")).isEmpty)
    #expect(KnobFirmwareImage.oldBuilds([image("0.9.42"), image("0.9.43")], status: knob(on: "0.9.42")).isEmpty)
    #expect(KnobFirmwareImage.oldBuilds(releases, status: nil).isEmpty)
    #expect(KnobFirmwareImage.oldBuilds(releases, status: KnobOTAStatus()).isEmpty)
}

@Test func badgesMarkTheKnobLatestAndMissingELF() {
    let images = [image("0.9.9", elf: false), image("0.9.10-rc1", KnobFirmwareImage.test), image("0.9.10")]
    let status = knob(on: "0.9.9")
    #expect(KnobFirmwareImage.badges(for: images[0], in: images, status: status) == [.onKnob, .noELF])
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: status) == [])
    #expect(KnobFirmwareImage.badges(for: images[2], in: images, status: status) == [.latest])
    #expect(KnobFirmwareImage.badges(for: images[2], in: images, status: nil) == [.latest])
}

@Test func badgesMarkTheUpdateTargetAsInstallingOrFailed() {
    let images = [image("0.9.9"), image("0.9.10")]
    let busy = knob(on: "0.9.9", target: "0.9.10", phase: .downloading, version: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: busy) == [.latest, .installing])
    let waiting = knob(on: "0.9.9", target: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: waiting) == [.latest])
    let offered = knob(on: "0.9.9", target: "0.9.10", phase: .offered, version: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: offered) == [.latest])
    let verifying = knob(on: "0.9.9", target: "0.9.10", phase: .verifying, version: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: verifying) == [.latest, .installing])
    let failed = knob(on: "0.9.9", phase: .failed, version: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: failed) == [.latest, .failed])
    let rolledBack = knob(on: "0.9.9", phase: .rolledBack, version: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: rolledBack) == [.latest, .failed])
    #expect(KnobFirmwareImage.badges(for: images[0], in: images, status: rolledBack) == [.onKnob])
    let done = knob(on: "0.9.10", phase: .done, version: "0.9.10")
    #expect(KnobFirmwareImage.badges(for: images[1], in: images, status: done) == [.onKnob, .latest])
}
