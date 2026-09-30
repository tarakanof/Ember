import AppKit
import EmberKit

/// Runs the bot's single frame loop and feeds both the menu-bar status item and
/// the Dock tile.
///
/// The loop renders at 24 fps only while `BotBehavior` reports motion (a blink,
/// a glance, a hop) and otherwise sleeps until the next scheduled event, so an
/// idle bot costs a wake-up every few seconds, not a timer per frame. It stops
/// entirely while nothing on screen shows the bot. A frame that would
/// rasterise the same as the last one pushed is skipped: each push costs a
/// status-item relayout and an XPC round-trip to the menu bar.
///
/// Frames go straight to the `NSStatusBarButton`, not through SwiftUI. Letting
/// the `MenuBarExtra` label observe a per-frame value leaked SwiftUI's
/// Observation registrations (about 600 MB after 3.5 days) and kept 3-8% CPU
/// busy in AttributeGraph re-evaluating the label. Deliberately not
/// `@Observable`, so nothing can start observing `pose` again.
@MainActor
final class BotAnimator {
    static let shared = BotAnimator()

    private(set) var pose = BotPose()

    private var behavior: BotBehavior
    private var loop: Task<Void, Never>?
    private let dockView = BotDockView()
    private var dockEnabled = false
    /// Mirrors `NSApp.activationPolicy()`, which is an XPC round-trip per call.
    /// Kept current by `activationPolicyDidChange(_:)`.
    private var isRegular = NSApplication.shared.activationPolicy() == .regular
    private var menuBarEnabled = false
    private var menuBarColored = true
    private weak var statusButton: NSStatusBarButton?
    /// Menu-bar colour crossfade on mood changes; nil = the menu bar's own
    /// foreground (the template look).
    private var tintFrom: NSColor?
    private var tintTo: NSColor?
    private var tintStart = -Double.infinity
    private static let tintFade = 0.35
    /// Everyday blinks and glances; 24 fps is smooth for them at 16 pt.
    private static let frameInterval = 1.0 / 24
    /// The 0.7 s mood morph glides, so it gets the display's rate.
    private static let morphFrameInterval = 1.0 / 60
    /// The 16 pt ball's radius on the 44 px menu-bar raster.
    private static let menuBarRadius = BotStyle.menuBar(tint: NSColor.black.cgColor).bodyRadius(side: 44)

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
        let t = Self.now
        let from = tint(at: t)
        guard behavior.setMood(BotMood(state: state), at: t) else { return }
        tintFrom = from
        tintTo = Self.tint(for: behavior.mood)
        tintStart = t
        restart()
    }

    /// The menu-bar colour at `t`, mid-fade included; nil = template foreground.
    private func tint(at t: Double) -> NSColor? {
        let k = (t - tintStart) / Self.tintFade
        guard k < 1 else { return tintTo }
        let fg = Self.menuBarForeground()
        let a = tintFrom ?? fg, b = tintTo ?? fg
        return a.blended(withFraction: max(k, 0), of: b) ?? b
    }

    /// Whether the menu-bar label shows the bot (vs the tool glyphs), and in
    /// colour or as a template. Called on every prefs change, which also
    /// re-renders the label and overwrites the button image, so it always
    /// restarts the loop to push a fresh frame.
    func showInMenuBar(_ on: Bool, colored: Bool) {
        menuBarEnabled = on
        menuBarColored = colored
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

    /// Call after every `setActivationPolicy`. A promotion must be reported
    /// before `showInDock`, whose restart reads it.
    func activationPolicyDidChange(_ policy: NSApplication.ActivationPolicy) {
        isRegular = policy == .regular
    }

    /// The Dock tile is only on screen while a window promotes us to .regular.
    private var dockVisible: Bool { dockEnabled && isRegular }

    private func restart() {
        loop?.cancel()
        loop = Task { [weak self] in await self?.run() }
    }

    /// Pushes a menu-bar frame only when its quantised pose changes, or on
    /// every frame of a colour fade plus once when the fade ends: that last
    /// push carries the final colour, which a held-still pose would skip.
    private func run() async {
        // Demotion to .accessory ends the loop here; the next promotion
        // re-applies the icon (AppDelegate → applyAppIcon), which restarts it.
        // The first frame is always pushed: a restart usually follows a label
        // re-render that replaced the button image.
        var menuBarStale = true
        var menuBarShown: BotPose?, dockShown: BotPose?
        var wasFading = false
        let dockScale = NSScreen.main?.backingScaleFactor ?? 2
        let dockRadius = BotStyle.dock(badge: nil)
            .bodyRadius(side: NSApplication.shared.dockTile.size.width * dockScale)
        while !Task.isCancelled && (menuBarEnabled || dockVisible) {
            let t = Self.now
            pose = behavior.pose(at: t)
            let fading = menuBarColored && t - tintStart < Self.tintFade
            if wasFading && !fading { menuBarStale = true }
            wasFading = fading
            var menuBarKey = pose.quantized(toPixels: Self.menuBarRadius)
            menuBarKey.badge = 0   // the menu-bar style draws no badge
            if menuBarKey != menuBarShown {
                menuBarShown = menuBarKey
                menuBarStale = true
            }
            let dockKey = pose.quantized(toPixels: dockRadius)
            if dockKey != dockShown {
                dockShown = dockKey
                renderDock()
            }
            if menuBarEnabled && (menuBarStale || fading) {
                menuBarStale = !pushMenuBar()
            }
            var wait = behavior.isTransitioning ? Self.morphFrameInterval
                : behavior.isAnimating || fading ? Self.frameInterval
                : min(max(behavior.nextEventAt - t, Self.frameInterval), 10)
            // No status item yet (early launch): retry soon, not at the next event.
            if menuBarEnabled && menuBarStale { wait = min(wait, 0.25) }
            try? await Task.sleep(for: .seconds(wait))
        }
    }

    /// Sets the current frame on the status item button. False while the
    /// `MenuBarExtra` hasn't created it yet.
    private func pushMenuBar() -> Bool {
        if statusButton == nil { statusButton = StatusItemButton.find() }
        guard let button = statusButton else { return false }
        button.image = menuBarImage(colored: menuBarColored)
        return true
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
    /// otherwise active moods keep the per-state colour cue, crossfading on change.
    func menuBarImage(colored: Bool) -> NSImage {
        Self.menuBarImage(pose, tint: colored ? tint(at: Self.now) : nil)
    }

    /// The colour template menu-bar images end up drawn in, so a crossfade to or
    /// from the template look starts and ends where the menu bar would put it.
    private static func menuBarForeground() -> NSColor {
        let bar = NSApplication.shared.windows.first { $0.className == "NSStatusBarWindow" }
        let appearance = bar?.contentView?.effectiveAppearance ?? NSApplication.shared.effectiveAppearance
        return appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? .white : .black
    }

    private static func menuBarImage(_ pose: BotPose, tint: NSColor?) -> NSImage {
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
