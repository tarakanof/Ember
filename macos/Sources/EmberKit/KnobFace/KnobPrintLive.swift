import SwiftUI

public struct KnobPrintLive: View {
    public static let fps = 15.0

    let state: PrinterState?
    let animated: Bool
    let windowVisible: Bool
    let theme: KnobTheme
    let brightness: Double
    @State private var visible = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(state: PrinterState?, animated: Bool = true, windowVisible: Bool = true, theme: KnobTheme = .standard,
                brightness: Double = 1) {
        self.state = state; self.animated = animated; self.windowVisible = windowVisible; self.theme = theme
        self.brightness = brightness
    }

    public static func ticks(animated: Bool, onScreen: Bool, windowVisible: Bool) -> Bool {
        animated && onScreen && windowVisible
    }

    public static func moves(_ face: KnobPrintFace, reduceMotion: Bool) -> Bool { face.moves && !reduceMotion }

    public var body: some View {
        let block = KnobPrintBlock(state: state)
        Group {
            if Self.ticks(animated: animated, onScreen: visible, windowVisible: windowVisible) {
                if Self.moves(face(block, at: .now), reduceMotion: reduceMotion) {
                    TimelineView(.animation(minimumInterval: 1 / Self.fps)) { tl in
                        let t = (tl.date.timeIntervalSinceReferenceDate * Self.fps).rounded(.down) / Self.fps
                        view(face(block, at: tl.date), t: t)
                    }
                } else {
                    TimelineView(.periodic(from: .now, by: 1)) { tl in view(face(block, at: tl.date), t: nil) }
                }
            } else {
                view(face(block, at: .now), t: nil)
            }
        }
        .onAppear { visible = true }
        .onDisappear { visible = false }
    }

    private func face(_ block: KnobPrintBlock?, at now: Date) -> KnobPrintFace {
        KnobPrintFace(block: block, now: now, theme: theme.print)
    }

    private func view(_ f: KnobPrintFace, t: Double?) -> some View {
        KnobFaceView(.print(f), theme: theme, brightness: brightness, t: t).equatable()
    }
}
