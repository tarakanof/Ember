import CoreGraphics
import Foundation

func sameImage(_ a: CGImage, _ b: CGImage) -> Bool {
    func bytes(_ img: CGImage) -> [UInt8] {
        var buf = [UInt8](repeating: 0, count: img.width * img.height * 4)
        let ctx = CGContext(data: &buf, width: img.width, height: img.height, bitsPerComponent: 8, bytesPerRow: img.width * 4,
                            space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(img, in: CGRect(x: 0, y: 0, width: img.width, height: img.height))
        return buf
    }
    guard a.width == b.width, a.height == b.height else { return false }
    let off = zip(bytes(a), bytes(b)).filter { $0 != $1 }
    return off.count <= 16 && off.allSatisfy { abs(Int($0) - Int($1)) <= 1 }
}
