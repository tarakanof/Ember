import AppKit
import EmberKit
import Observation

/// Runs the bot's single frame loop and feeds both the menu-bar label (via the
/// observed `pose`) and the Dock tile.
///
/// The loop renders at 30 fps only while `BotBehavior` reports motion (a blink,
/// a glance, a hop) and otherwise sleeps until the next scheduled event, so an
/// idle bot costs a wake-up every few seconds, not a timer per frame. It stops
/// entirely while nothing on screen shows the bot.
@MainActor @Observable
final class BotAnimator {
    static let shared = BotAnimator()

    private(set) var pose = BotPose()

    @ObservationIgnored private var behavior: BotBehavior
    @ObservationIgnored private var loop: Task<Void, Never>?
    @ObservationIgnored private let dockView = BotDockView()
    @ObservationIgnored private var dockEnabled = false
    @ObservationIgnored private var menuBarEnabled = false

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
        if behavior.setMood(BotMood(state: state), at: Self.now) { restart() }
    }

    /// Whether the menu-bar label shows the bot (vs the tool glyphs).
    func showInMenuBar(_ on: Bool) {
        guard on != menuBarEnabled else { return }
        menuBarEnabled = on
        restart()
    }

    /// Swaps the Dock tile between the live bot and the static bundle icon.
    func showInDock(_ on: Bool) {
        dockEnabled = on
        let app = NSApplication.shared
        let tile = app.dockTile
        if on {
            // Cmd-Tab and Finder read the icon image, not the tile view. Set it
            // first: assigning it afterwards replaces the live content view.
            app.applicationIconImage = Self.staticIcon()
            dockView.frame = NSRect(origin: .zero, size: tile.size)
            dockView.pose = pose
            tile.contentView = dockView
        } else {
            tile.contentView = nil
        }
        tile.display()
        restart()
    }

    /// The Dock tile is only on screen while a window promotes us to .regular.
    private var dockVisible: Bool {
        dockEnabled && NSApplication.shared.activationPolicy() == .regular
    }

    private func restart() {
        loop?.cancel()
        loop = Task { [weak self] in await self?.run() }
    }

    private func run() async {
        // Demotion to .accessory ends the loop here; the next promotion
        // re-applies the icon (AppDelegate → applyAppIcon), which restarts it.
        while !Task.isCancelled && (menuBarEnabled || dockVisible) {
            let t = Self.now
            let p = behavior.pose(at: t)
            if p != pose {
                pose = p
                renderDock()
            }
            // 30 fps: each frame re-rasterises the status item; 60 doubles the CPU
            // for no visible gain at 16 pt.
            let wait = behavior.isAnimating ? 1.0 / 30 : min(max(behavior.nextEventAt - t, 1.0 / 30), 10)
            try? await Task.sleep(for: .seconds(wait))
        }
    }

    private func renderDock() {
        guard dockVisible else { return }
        let tile = NSApplication.shared.dockTile
        if tile.contentView !== dockView { tile.contentView = dockView }
        dockView.pose = pose
        dockView.needsDisplay = true
        tile.display()
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

    /// Idle/sleepy — and every mood when `colored` is off — render as a template
    /// (black or white to match the menu bar, like the reference's black ball);
    /// otherwise active moods keep the per-state colour cue.
    static func menuBarImage(_ pose: BotPose, colored: Bool) -> NSImage {
        let tint = colored ? tint(for: pose.mood) : nil
        let body = tint?.cgColor ?? NSColor.black.cgColor
        // Rasterised up front: the menu bar treats a lazily drawn NSImage as a
        // template and drops the colour.
        let pt = 22, px = pt * 2
        guard let ctx = CGContext(data: nil, width: px, height: px, bitsPerComponent: 8, bytesPerRow: 0,
                                  space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return NSImage() }
        BotRenderer.draw(pose, in: ctx, rect: CGRect(x: 0, y: 0, width: px, height: px), style: .menuBar(tint: body))
        guard let cg = ctx.makeImage() else { return NSImage() }
        let img = NSImage(cgImage: cg, size: NSSize(width: pt, height: pt))
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
