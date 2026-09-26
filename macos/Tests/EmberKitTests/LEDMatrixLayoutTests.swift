import Testing
import CoreGraphics
@testable import EmberKit

/// Width-only containers, as a Form row or card offers: pitch is the whole
/// points that fit, capped at the maximum, and the panel is centred.
@Test(arguments: [
    (CGFloat(300), CGFloat(9), CGFloat(6)), // 288 wide
    (520, 16, 4),          // 512 wide
    (700, 20, 30),         // capped: 640 wide
    (1100, 20, 230),
    (256, 8, 0),           // exact fit
])
func widthOnlyContainer(width: CGFloat, pitch: CGFloat, originX: CGFloat) {
    let l = LEDMatrixLayout(width: width, height: nil)
    #expect(l.pitch == pitch)
    #expect(l.size == CGSize(width: pitch * 32, height: pitch * 8))
    #expect(l.origin == CGPoint(x: originX, y: 0))
}

@Test func pitchIsAlwaysAWholeNumberAndPanelStaysFourToOne() {
    for w in stride(from: 90.0, through: 1200, by: 7.3) {
        let l = LEDMatrixLayout(width: w, height: w / 3.1)
        #expect(l.pitch == l.pitch.rounded(), "width \(w)")
        #expect(l.size.width == l.size.height * 4, "width \(w)")
        #expect(l.size.width <= max(w, 96), "width \(w)")
    }
}

@Test func tighterAxisWins() {
    // 700×100: height allows 12, width 21 → 12, centred both ways.
    let l = LEDMatrixLayout(width: 700, height: 100)
    #expect(l.pitch == 12)
    #expect(l.size == CGSize(width: 384, height: 96))
    #expect(l.origin == CGPoint(x: 158, y: 2))
}

@Test func nonIntegerContainerSnapsOriginToDevicePixels() {
    let at1x = LEDMatrixLayout(width: 333.7, height: nil, scale: 1)
    #expect(at1x.pitch == 10)
    #expect(at1x.origin.x == 6)
    let at2x = LEDMatrixLayout(width: 333.7, height: 81.3, scale: 2)
    #expect(at2x.pitch == 10)
    #expect(at2x.origin == CGPoint(x: 6.5, y: 0.5))
}

@Test func tinyContainerClampsToMinimumPitch() {
    let l = LEDMatrixLayout(width: 40, height: 5)
    #expect(l.pitch == LEDMatrixLayout.minPitch)
    #expect(l.size == CGSize(width: 96, height: 24))
    #expect(l.origin.x < 0) // overflows, still centred
    #expect(!l.showsGlow)
}

@Test func hugeOrUnboundedContainerCapsPitch() {
    #expect(LEDMatrixLayout(width: 10_000, height: 10_000).pitch == LEDMatrixLayout.maxPitch)
    let open = LEDMatrixLayout(width: .infinity, height: .infinity)
    #expect(open.pitch == LEDMatrixLayout.maxPitch)
    #expect(open.origin == .zero)
    #expect(LEDMatrixLayout(width: .infinity, height: 80).pitch == 10)
}

@Test func noProposalUsesIdealPitch() {
    #expect(LEDMatrixLayout(width: nil, height: nil).pitch == LEDMatrixLayout.idealPitch)
    #expect(LEDMatrixLayout(width: nil, height: nil, maxPitch: 7).pitch == 7)
}

@Test func callerCapLimitsPitch() {
    #expect(LEDMatrixLayout(width: 1000, height: nil, maxPitch: 14).pitch == 14)
    #expect(LEDMatrixLayout(width: 1000, height: nil, maxPitch: 14.9).pitch == 14)
}

@Test func ledsSitOnWholePixelsInsideTheirCells() {
    for pitch: CGFloat in [3, 6, 9, 16, 20] {
        let l = LEDMatrixLayout(width: pitch * 32, height: nil)
        let led = l.led(x: 5, y: 3)
        // On whole Retina pixels; whole points once the pitch allows a 1 pt gap.
        #expect((led.minX * 2).rounded() == led.minX * 2 && (led.width * 2).rounded() == led.width * 2,
                "pitch \(pitch)")
        if pitch >= 4 { #expect(led.minX == led.minX.rounded() && l.gap >= 1, "pitch \(pitch)") }
        #expect(led.width >= pitch / 2 && l.cell(x: 5, y: 3).contains(led), "pitch \(pitch)")
    }
    #expect(LEDMatrixLayout(width: 192, height: nil).showsGlow)
    #expect(!LEDMatrixLayout(width: 160, height: nil).showsGlow)
}
