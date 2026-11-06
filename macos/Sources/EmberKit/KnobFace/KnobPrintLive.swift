import SwiftUI

public struct KnobPrintLive: View {
    nonisolated public static let fps = 15.0

    let state: PrinterState?
    let link: KnobPrintFace.Link
    let animated: Bool
    let windowVisible: Bool
    let theme: KnobTheme
    let brightness: Double
    @State private var visible = false
    @State private var doneSince: Date?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(state: PrinterState?, link: KnobPrintFace.Link = .fed, animated: Bool = true, windowVisible: Bool = true,
                theme: KnobTheme = .standard, brightness: Double = 1) {
        self.state = state; self.link = link; self.animated = animated; self.windowVisible = windowVisible; self.theme = theme
        self.brightness = brightness
    }

    nonisolated public static func ticks(animated: Bool, onScreen: Bool, windowVisible: Bool) -> Bool {
        animated && onScreen && windowVisible
    }

    nonisolated public static func moves(_ face: KnobPrintFace, reduceMotion: Bool) -> Bool { face.moves && !reduceMotion }

    public struct DoneKey: Equatable, Sendable {
        public var job: Int64?
        public var finishedAt: Date?
    }

    nonisolated public static func doneKey(_ block: KnobPrintBlock?) -> DoneKey {
        let finished = block?.event.flatMap { $0.kind == "finished" ? Date(timeIntervalSince1970: TimeInterval($0.at)) : nil }
        return DoneKey(job: doneJob(block), finishedAt: finished)
    }

    nonisolated public static func doneJob(_ block: KnobPrintBlock?) -> Int64? {
        guard let b = block, b.phase == .finished else { return nil }
        return b.startedAt ?? b.event.flatMap { $0.kind == "finished" ? $0.at : nil } ?? 0
    }

    public var body: some View {
        let block = KnobPrintBlock(state: state)
        Group {
            if Self.ticks(animated: animated, onScreen: visible, windowVisible: windowVisible) {
                TimelineView(.periodic(from: .now, by: 1)) { tl in
                    let f = face(block, at: tl.date)
                    if Self.moves(f, reduceMotion: reduceMotion) {
                        TimelineView(.animation(minimumInterval: 1 / Self.fps)) { frame in
                            view(f, t: (frame.date.timeIntervalSinceReferenceDate * Self.fps).rounded(.down) / Self.fps)
                        }
                    } else {
                        view(f, t: nil)
                    }
                }
            } else {
                view(face(block, at: .now), t: nil)
            }
        }
        .onAppear { visible = true }
        .onDisappear { visible = false }
        .onChange(of: Self.doneKey(block), initial: true) { _, key in
            doneSince = KnobPrintDoneClock.shared.since(key.job, finishedAt: key.finishedAt, now: .now)
        }
    }

    private func face(_ block: KnobPrintBlock?, at now: Date) -> KnobPrintFace {
        KnobPrintFace(block: block, now: now, link: link, doneSince: doneSince, theme: theme)
    }

    private func view(_ f: KnobPrintFace, t: Double?) -> some View {
        KnobFaceView(.print(f), theme: theme, brightness: brightness, t: t).equatable()
    }
}

@MainActor
public final class KnobPrintDoneClock {
    public static let shared = KnobPrintDoneClock()
    private var job: Int64 = 0
    private var since: Date?

    public init() {}

    public func since(_ job: Int64?, finishedAt: Date?, now: Date) -> Date? {
        guard let job else {
            since = nil
            return nil
        }
        if since != nil, job != 0, self.job != 0, job != self.job { since = nil }
        if job != 0 { self.job = job }
        let t0 = finishedAt ?? now
        if let s = since, s <= t0 { return s }
        since = t0
        return t0
    }
}
