import AppKit
import EmberKit
import Observation

/// Runs the bot's single frame loop and feeds both the menu-bar label (via the
/// observed `pose`) and the Dock tile.
///
/// The loop renders at 60 fps only while `BotBehavior` reports motion (a blink,
/// a glance, a hop) and otherwise sleeps until the next scheduled event, so an
/// idle bot costs a wake-up every few seconds, not a timer per frame.
@MainActor @Observable
final class BotAnimator {
    static let shared = BotAnimator()

    private(set) var pose = BotPose()

    @ObservationIgnored private var behavior: BotBehavior
    @ObservationIgnored private var loop: Task<Void, Never>?
    @ObservationIgnored private let dockView = BotDockView()
    @ObservationIgnored private var dockEnabled = false

    private init() {
        behavior = BotBehavior(seed: .random(in: 0 ... .max), now: Self.now)
        behavior.reduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification,
            object: nil, queue: .main
        ) { _ in
            Task { @MainActor in
                let bot = BotAnimator.shared
                bot.behavior.reduceMotion = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
                bot.restart()
            }
        }
        restart()
    }

    private static var now: Double { ProcessInfo.processInfo.systemUptime }

    /// Feeds the winning session's state; a no-op when the mood is unchanged.
    func setState(_ state: String) {
        let mood = BotMood(state: state)
        guard mood != behavior.mood else { return }
        behavior.setMood(mood, at: Self.now)
        restart()
    }

    /// Swaps the Dock tile between the live bot and the static bundle icon.
    func showInDock(_ on: Bool) {
        dockEnabled = on
        let tile = NSApp.dockTile
        if on {
            dockView.frame = NSRect(origin: .zero, size: tile.size)
            dockView.pose = pose
            tile.contentView = dockView
            // Cmd-Tab and Finder read the icon image, not the tile view.
            NSApp.applicationIconImage = Self.staticIcon()
        } else {
            tile.contentView = nil
        }
        tile.display()
    }

    private func restart() {
        loop?.cancel()
        loop = Task { [weak self] in await self?.run() }
    }

    private func run() async {
        while !Task.isCancelled {
            let t = Self.now
            let p = behavior.pose(at: t)
            if p != pose {
                pose = p
                renderDock()
            }
            let wait = behavior.isAnimating ? 1.0 / 60 : min(max(behavior.nextEventAt - t, 1.0 / 60), 10)
            try? await Task.sleep(for: .seconds(wait))
        }
    }

    private func renderDock() {
        // The tile is only on screen while a window promotes us to .regular.
        guard dockEnabled, NSApp.activationPolicy() == .regular else { return }
        dockView.pose = pose
        NSApp.dockTile.display()
    }

    // MARK: - Images

    /// The session colour a mood stands for; nil for the monochrome moods.
    static func tint(for mood: BotMood) -> NSColor? {
        switch mood {
        case .idle, .sleepy: return nil
        case .working:       return color(stateColorRGB("running"))
        case .waiting:       return color(stateColorRGB("waiting"))
        case .error:         return color(stateColorRGB("error"))
        case .done:          return color(stateColorRGB("done"))
        }
    }

    /// Idle/sleepy render as a template (black or white to match the menu bar,
    /// like the reference's black ball); active moods keep the per-state colour
    /// cue the tool glyphs always had.
    static func menuBarImage(_ pose: BotPose) -> NSImage {
        let tint = tint(for: pose.mood)
        let body = tint?.cgColor ?? NSColor.black.cgColor
        // Rasterised up front: the menu bar treats a lazily drawn NSImage as a
        // template and drops the colour.
        let px = 36
        guard let ctx = CGContext(data: nil, width: px, height: px, bitsPerComponent: 8, bytesPerRow: 0,
                                  space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return NSImage() }
        BotRenderer.draw(pose, in: ctx, rect: CGRect(x: 0, y: 0, width: px, height: px), style: .menuBar(tint: body))
        guard let cg = ctx.makeImage() else { return NSImage() }
        let img = NSImage(cgImage: cg, size: NSSize(width: 18, height: 18))
        img.isTemplate = tint == nil
        return img
    }

    static func staticIcon(size: CGFloat = 512) -> NSImage {
        NSImage(size: NSSize(width: size, height: size), flipped: false) { rect in
            guard let ctx = NSGraphicsContext.current?.cgContext else { return false }
            BotRenderer.draw(BotPose(), in: ctx, rect: rect, style: .dock(badge: nil))
            return true
        }
    }

    private static func color(_ rgb: RGB) -> NSColor {
        NSColor(srgbRed: CGFloat(rgb.r) / 255, green: CGFloat(rgb.g) / 255, blue: CGFloat(rgb.b) / 255, alpha: 1)
    }
}

/// The Dock tile's content: redrawn by `NSDockTile.display()` on each frame.
final class BotDockView: NSView {
    var pose = BotPose()

    override func draw(_ dirtyRect: NSRect) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        let badge = BotAnimator.tint(for: pose.mood)?.cgColor
        BotRenderer.draw(pose, in: ctx, rect: bounds, style: .dock(badge: badge))
    }
}
