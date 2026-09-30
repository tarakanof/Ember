import Testing
import CoreGraphics
@testable import EmberKit

/// The menu bar's 16 pt ball on a 44 px (2x) canvas.
private let menuBarRadius = BotStyle.menuBar(tint: CGColor(gray: 0, alpha: 1)).bodyRadius(side: 44)

@Test func menuBarBodyRadiusIsSixteenPixels() {
    #expect(abs(menuBarRadius - 16) < 1e-9)
}

@Test func subPixelPoseChangesQuantiseEqual() {
    var raw = BotPose()
    raw.gazeX = 0.40; raw.gazeY = 0.20; raw.lidLeft = 0.30; raw.lidRight = 0.31
    raw.triangle = 0.5; raw.slump = 0.2; raw.badge = 0.6
    raw.scaleX = 1.02; raw.scaleY = 0.98; raw.offsetX = 0.03; raw.offsetY = -0.02
    // Start on step centres so a small nudge can't straddle a rounding edge.
    let a = raw.quantized(toPixels: menuBarRadius)
    #expect(a.quantized(toPixels: menuBarRadius) == a)
    // A tenth of a pixel's worth on every field.
    let d = 0.1 / menuBarRadius
    var b = a
    b.gazeX += d; b.gazeY -= d; b.lidLeft += d; b.lidRight -= d
    b.triangle += d; b.slump += d; b.badge -= d
    b.scaleX += d / 2; b.scaleY -= d / 2; b.offsetX += d; b.offsetY -= d
    #expect(a != b)
    #expect(b.quantized(toPixels: menuBarRadius) == a)
}

@Test func onePixelGazeStepQuantisesDifferent() {
    let a = BotPose()
    var b = a
    // The eyes travel 0.6 radii per unit of gaze, so this moves them 1 px.
    b.gazeX += 1 / (0.6 * menuBarRadius)
    #expect(a.quantized(toPixels: menuBarRadius) != b.quantized(toPixels: menuBarRadius))
}

@Test func onePixelBlinkStepQuantisesDifferent() {
    let a = BotPose()
    var b = a
    // Over a blink a dash eye loses about 0.32 x 1.25 radii of height, plus
    // eyeGrow, so this shuts it by at least 1 px in total, 0.5 px per edge.
    b.lidLeft += 1 / (0.32 * 1.25 * menuBarRadius)
    #expect(a.quantized(toPixels: menuBarRadius) != b.quantized(toPixels: menuBarRadius))
}

@Test func moodAndEyeChangesAlwaysQuantiseDifferent() {
    let a = BotPose()
    var mood = a
    mood.mood = .working
    var eyes = a
    eyes.eyes = .round
    #expect(a.quantized(toPixels: menuBarRadius) != mood.quantized(toPixels: menuBarRadius))
    #expect(a.quantized(toPixels: menuBarRadius) != eyes.quantized(toPixels: menuBarRadius))
}

@Test func largerCanvasesKeepFinerSteps() {
    let a = BotPose().quantized(toPixels: menuBarRadius)
    var b = a
    b.gazeX += 0.01
    // 0.1 px of eye travel in the menu bar, 0.8 px on a 128 px Dock ball.
    #expect(a.quantized(toPixels: menuBarRadius) == b.quantized(toPixels: menuBarRadius))
    #expect(a.quantized(toPixels: 128) != b.quantized(toPixels: 128))
}

/// Half a pixel at the menu-bar radius, in body radii.
private let step = 0.5 / menuBarRadius

@Test func exactlyOneStepQuantisesDifferent() {
    // From one step up, a whole-pixel (1/r) step would round both to 2 steps.
    var a = BotPose()
    a.gazeX = step; a.offsetX = step
    var gaze = a
    gaze.gazeX += step
    var offset = a
    offset.offsetX += step
    #expect(a.quantized(toPixels: menuBarRadius) != gaze.quantized(toPixels: menuBarRadius))
    #expect(a.quantized(toPixels: menuBarRadius) != offset.quantized(toPixels: menuBarRadius))
}

@Test func justUnderHalfAStepFromABinCentreQuantisesEqual() {
    var a = BotPose()
    a.gazeX = 3 * step; a.lidLeft = 3 * step
    var b = a
    b.gazeX += 0.49 * step; b.lidLeft -= 0.49 * step
    #expect(a.quantized(toPixels: menuBarRadius) == b.quantized(toPixels: menuBarRadius))
}
