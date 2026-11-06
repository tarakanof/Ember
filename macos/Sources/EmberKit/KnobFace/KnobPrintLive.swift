import SwiftUI

public struct KnobPrintLive: View {
    public static let fps = 15.0

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

    public static func ticks(animated: Bool, onScreen: Bool, windowVisible: Bool) -> Bool {
        animated && onScreen && windowVisible
    }

    public static func moves(_ face: KnobPrintFace, reduceMotion: Bool) -> Bool { face.moves && !reduceMotion }

    public static func doneJob(_ block: KnobPrintBlock?) -> Int64? {
        block?.phase == .finished ? block?.startedAt ?? 0 : nil
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
        .onChange(of: Self.doneJob(block), initial: true) { _, job in doneSince = job == nil ? nil : .now }
    }

    private func face(_ block: KnobPrintBlock?, at now: Date) -> KnobPrintFace {
        KnobPrintFace(block: block, now: now, link: link, doneSince: doneSince, theme: theme)
    }

    private func view(_ f: KnobPrintFace, t: Double?) -> some View {
        KnobFaceView(.print(f), theme: theme, brightness: brightness, t: t).equatable()
    }
}
