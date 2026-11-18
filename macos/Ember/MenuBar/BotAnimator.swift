import AppKit
import EmberKit

@MainActor
final class BotAnimator {
    static let shared = BotAnimator()

    private(set) var pose = BotPose()

    private var behavior: BotBehavior
    private var loop: Task<Void, Never>?
    private let dockView = BotDockView()
    private var dockEnabled = false
    private var isRegular = NSApplication.shared.activationPolicy() == .regular
    private var menuBarEnabled = false
    private var menuBarColored = true
    private weak var statusButton: NSStatusBarButton?
    static let menuBarPlaceholder = NSImage(size: NSSize(width: 22, height: 22))
    private let botLayer: BotMenuBarLayer = {
        let layer = BotMenuBarLayer()
        layer.actions = ["contents": NSNull(), "position": NSNull(), "bounds": NSNull(), "hidden": NSNull()]
        layer.contentsScale = 2
        return layer
    }()
    private var drawnForeground: NSColor?
    private let liveContext = CGContext(data: nil, width: 44, height: 44, bitsPerComponent: 8, bytesPerRow: 0,
                                        space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
    private var tintFrom: NSColor?
    private var tintTo: NSColor?
    private var tintStart = -Double.infinity
    private static let tintFade = 0.35
    private static let frameInterval = 1.0 / 24
    private static let morphFrameInterval = 1.0 / 60
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

    func setState(_ state: String) {
        let t = Self.now
        let from = tint(at: t)
        guard behavior.setMood(BotMood(state: state), at: t) else { return }
        tintFrom = from
        tintTo = Self.tint(for: behavior.mood)
        tintStart = t
        restart()
    }

    private func tint(at t: Double) -> NSColor? {
        let k = (t - tintStart) / Self.tintFade
        guard k < 1 else { return tintTo }
        let fg = menuBarForeground()
        let a = tintFrom ?? fg, b = tintTo ?? fg
        return a.blended(withFraction: max(k, 0), of: b) ?? b
    }

    func setPrint(_ print: BotPrint) {
        let before = behavior.print
        behavior.setPrint(print, at: Self.now)
        guard !behavior.print.looksLike(before, radius: Self.menuBarRadius) else { return }
        restart()
    }

    func showInMenuBar(_ on: Bool, colored: Bool) {
        menuBarEnabled = on
        menuBarColored = colored
        if !on { botLayer.removeFromSuperlayer() }
        restart()
    }

    func showInDock(_ on: Bool) {
        dockEnabled = on
        let app = NSApplication.shared
        let tile = app.dockTile
        if on {
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

    var onDockVisibilityChange: ((Bool) -> Void)?

    var isDockVisible: Bool { isRegular }

    func activationPolicyDidChange(_ policy: NSApplication.ActivationPolicy) {
        let regular = policy == .regular
        guard regular != isRegular else { return }
        isRegular = regular
        onDockVisibilityChange?(regular)
    }

    private var dockVisible: Bool { dockEnabled && isRegular }

    private func restart() {
        loop?.cancel()
        loop = Task { [weak self] in await self?.run() }
    }

    private func run() async {
        var menuBarStale = true
        var menuBarShown: BotPose?, dockShown: BotPose?, stillShown: BotPose?
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
            menuBarKey.badge = 0
            if menuBarKey != menuBarShown {
                menuBarShown = menuBarKey
                menuBarStale = true
            }
            let dockKey = pose.quantized(toPixels: dockRadius)
            if dockKey != dockShown {
                dockShown = dockKey
                renderDock()
            }
            if let drawn = drawnForeground, menuBarForeground() != drawn { menuBarStale = true }
            var stillKey = behavior.still.quantized(toPixels: Self.menuBarRadius)
            stillKey.badge = 0
            let still = stillKey != stillShown ? behavior.still : nil
            if menuBarEnabled && (menuBarStale || fading || still != nil) {
                menuBarStale = !pushMenuBar(still: still)
                if !menuBarStale && still != nil { stillShown = stillKey }
            }
            var wait = behavior.isTransitioning ? Self.morphFrameInterval
                : behavior.isAnimating || fading ? Self.frameInterval
                : min(max(behavior.nextEventAt - t, Self.frameInterval), 10)
            if menuBarEnabled && menuBarStale { wait = min(wait, 0.25) }
            try? await Task.sleep(for: .seconds(wait))
        }
    }

    private func pushMenuBar(still: BotPose?) -> Bool {
        if statusButton == nil { statusButton = StatusItemButton.find() }
        let fg = menuBarForeground()
        drawnForeground = fg
        guard let button = statusButton,
              let frame = drawFrame(pose, tint: menuBarColored ? tint(at: Self.now) : nil, foreground: fg)
        else { return false }
        if let still {
            let tint = menuBarColored ? Self.tint(for: still.mood) : nil
            botLayer.stillOnDark = drawFrame(still, tint: tint, foreground: .white)
            botLayer.stillOnLight = drawFrame(still, tint: tint, foreground: .black)
        }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        if botLayer.superlayer == nil || botLayer.superlayer !== button.layer {
            button.wantsLayer = true
            button.layer?.addSublayer(botLayer)
        }
        let side = Self.menuBarPlaceholder.size.width
        let bounds = button.bounds
        let target = CGRect(x: ((bounds.width - side) / 2).rounded(), y: ((bounds.height - side) / 2).rounded(),
                            width: side, height: side)
        if botLayer.frame != target { botLayer.frame = target }
        botLayer.contents = frame
        CATransaction.commit()
        if still != nil { button.needsDisplay = true }
        return botLayer.superlayer != nil
    }

    private func drawFrame(_ pose: BotPose, tint: NSColor?, foreground fg: NSColor) -> CGImage? {
        var tint = tint
        if menuBarColored && tint == nil && pose.showsPrint { tint = fg }
        guard let ctx = liveContext else { return nil }
        let rect = CGRect(x: 0, y: 0, width: ctx.width, height: ctx.height)
        ctx.clear(rect)
        BotRenderer.draw(pose, in: ctx, rect: rect,
                         style: .menuBar(tint: tint?.cgColor ?? NSColor.black.cgColor, template: tint == nil))
        if tint == nil {
            ctx.saveGState()
            ctx.setBlendMode(.sourceIn)
            ctx.setFillColor(fg.cgColor)
            ctx.fill(rect)
            ctx.restoreGState()
        }
        return ctx.makeImage()
    }

    private func renderDock() {
        guard dockVisible else { return }
        let tile = NSApplication.shared.dockTile
        if tile.contentView !== dockView { tile.contentView = dockView }
        dockView.pose = pose
        dockView.needsDisplay = true
        tile.display()
    }

    static func tint(for mood: BotMood) -> NSColor? {
        switch mood {
        case .idle, .sleepy: return nil
        case .working:       return color(stateColorRGB("running"))
        case .waiting:       return color(stateColorRGB("waiting"))
        case .error:         return color(stateColorRGB("error"))
        case .done:          return color(stateColorRGB("done"))
        }
    }

    private func menuBarForeground() -> NSColor {
        let appearance = statusButton?.effectiveAppearance
            ?? NSApplication.shared.windows.first { $0.className == "NSStatusBarWindow" }?.contentView?.effectiveAppearance
            ?? NSApplication.shared.effectiveAppearance
        return appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? .white : .black
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

final class BotDockView: NSView {
    var pose = BotPose()

    override func draw(_ dirtyRect: NSRect) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        let badge = BotAnimator.tint(for: pose.mood)?.cgColor
        BotRenderer.draw(pose, in: ctx, rect: bounds, style: .dock(badge: badge))
    }
}

final class BotMenuBarLayer: CALayer {
    var stillOnDark: CGImage?
    var stillOnLight: CGImage?

    override func render(in ctx: CGContext) {
        let dark = MainActor.assumeIsolated {
            NSAppearance.currentDrawing().bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        }
        guard let still = dark ? stillOnDark : stillOnLight else { return super.render(in: ctx) }
        ctx.saveGState()
        ctx.translateBy(x: 0, y: bounds.height)
        ctx.scaleBy(x: 1, y: -1)
        ctx.draw(still, in: bounds)
        ctx.restoreGState()
    }
}
