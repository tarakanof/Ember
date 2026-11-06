import Foundation

@MainActor
public protocol KnobPageMarks: AnyObject {
    func isDecided(knob: String, page: String) -> Bool
    func markDecided(knob: String, page: String)
}

@MainActor
public final class MemoryKnobPageMarks: KnobPageMarks {
    private var decided: Set<String> = []

    public init() {}

    public func isDecided(knob: String, page: String) -> Bool { decided.contains("\(knob)/\(page)") }
    public func markDecided(knob: String, page: String) { decided.insert("\(knob)/\(page)") }
}

@MainActor
public final class DefaultsKnobPageMarks: KnobPageMarks {
    public static let key = "knob.pagesDecided"

    private let defaults: UserDefaults

    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    private var decided: Set<String> { Set(defaults.stringArray(forKey: Self.key) ?? []) }

    public func isDecided(knob: String, page: String) -> Bool { decided.contains("\(knob)/\(page)") }

    public func markDecided(knob: String, page: String) {
        defaults.set(decided.union(["\(knob)/\(page)"]).sorted(), forKey: Self.key)
    }
}

extension KnobModel {
    @discardableResult
    public func turnOnPrintPageIfUntouched() async -> Bool {
        guard let id = knob?.id, knob?.supports(page: KnobCaps.printPage) == true,
              !pageMarks.isDecided(knob: id, page: KnobCaps.printPage) else { return false }
        if !settings.isLoaded { await settings.load() }
        guard knob?.id == id, settings.isLoaded,
              let index = settings.draft.pages.firstIndex(where: { $0.id == KnobCaps.printPage }) else { return false }
        if settings.draft.pages[index].on {
            pageMarks.markDecided(knob: id, page: KnobCaps.printPage)
            return false
        }
        edit(byUser: false) { $0.pages[index].on = true }
        await settings.saveNow()
        guard settings.saveError == nil, settings.applied?.pages.first(where: { $0.id == KnobCaps.printPage })?.on == true
        else { return false }
        pageMarks.markDecided(knob: id, page: KnobCaps.printPage)
        return true
    }
}

@MainActor
public struct KnobPrintPageDefault {
    private let knob: KnobModel
    private let printerIsOn: () async -> Bool

    public init(knob: KnobModel, printerIsOn: @escaping () async -> Bool) {
        self.knob = knob
        self.printerIsOn = printerIsOn
    }

    public func printerSwitchedOn() async {
        if !knob.isLoaded { await knob.load() }
        await knob.turnOnPrintPageIfUntouched()
    }

    public func knobReported() async {
        guard let id = knob.knob?.id, knob.knob?.supports(page: KnobCaps.printPage) == true,
              !knob.pageMarks.isDecided(knob: id, page: KnobCaps.printPage) else { return }
        guard await printerIsOn() else { return }
        await knob.turnOnPrintPageIfUntouched()
    }
}
