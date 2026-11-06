import Foundation

public struct KnobNowReadout: Equatable, Sendable {
    public var rssi: Int?
    public var rssiMin: Int?
    public var heapFree: Int?
    public var heapLargest: Int?
    public var heapMin: Int?
    public var largestMin: Int?
    public var uptimeS: Int?

    public init(rssi: Int? = nil, rssiMin: Int? = nil, heapFree: Int? = nil, heapLargest: Int? = nil,
                heapMin: Int? = nil, largestMin: Int? = nil, uptimeS: Int? = nil) {
        self.rssi = rssi; self.rssiMin = rssiMin; self.heapFree = heapFree; self.heapLargest = heapLargest
        self.heapMin = heapMin; self.largestMin = largestMin; self.uptimeS = uptimeS
    }

    public init(latest: KnobStats.Sample?, checkin: KnobCheckin?) {
        let read = { (n: Int?) in n.flatMap { $0 == 0 ? nil : $0 } }
        self.init(rssi: read(latest?.rssiDBm) ?? read(checkin?.rssi),
                  rssiMin: read(checkin?.wifi?.rssiMin),
                  heapFree: read(latest?.heapInternalFreeBytes) ?? read(checkin?.heapInternalFree),
                  heapLargest: read(latest?.heapInternalLargestBytes) ?? read(checkin?.heapInternalLargest),
                  heapMin: read(checkin?.diag?.heapInternalMin),
                  largestMin: checkin?.diag?.largestBlockMin,
                  uptimeS: latest?.uptimeSec ?? checkin?.uptimeS)
    }
}
