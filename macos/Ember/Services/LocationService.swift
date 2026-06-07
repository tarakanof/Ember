import CoreLocation

/// One-shot current-location lookup for the Weather tab: requests when-in-use
/// authorization, gets a single fix, and reverse-geocodes a short place name.
@MainActor
public final class LocationService: NSObject, CLLocationManagerDelegate {
    public struct Fix: Sendable { public let latitude: Double; public let longitude: Double; public let name: String? }
    public enum LocationError: Error { case denied, unavailable }

    private let manager = CLLocationManager()
    private var continuation: CheckedContinuation<CLLocation, Error>?
    private var awaitingAuth = false
    private var watchdog: Task<Void, Never>?

    public override init() {
        super.init()
        manager.delegate = self
        manager.desiredAccuracy = kCLLocationAccuracyKilometer // weather doesn't need precision
    }

    /// Detect the current location + place name. Throws on denial/failure.
    public func current() async throws -> Fix {
        let loc = try await requestFix()
        let name = try? await reverseGeocode(loc)
        return Fix(latitude: loc.coordinate.latitude, longitude: loc.coordinate.longitude, name: name)
    }

    /// Resumes the in-flight continuation with `.unavailable` if neither a fix nor
    /// an authorization decision arrives in time (e.g. the user dismisses the
    /// prompt). Without this, a stuck request would orphan the continuation and
    /// wedge the UI. Cancels any prior watchdog so each request gets a fresh timer.
    private func startWatchdog() {
        watchdog?.cancel()
        watchdog = Task { @MainActor [weak self] in
            try? await Task.sleep(nanoseconds: 20_000_000_000)
            guard let self, let cont = self.continuation else { return }
            self.continuation = nil
            self.awaitingAuth = false
            cont.resume(throwing: LocationError.unavailable)
        }
    }

    private func requestFix() async throws -> CLLocation {
        guard continuation == nil else { throw LocationError.unavailable } // a detect is already in flight
        switch manager.authorizationStatus {
        case .denied, .restricted:
            throw LocationError.denied
        case .notDetermined:
            // Defer requestLocation() until the user answers the prompt (issuing it
            // now, while .notDetermined, does not reliably deliver a callback).
            return try await withCheckedThrowingContinuation { cont in
                self.continuation = cont
                self.awaitingAuth = true
                self.startWatchdog()
                manager.requestWhenInUseAuthorization()
            }
        default:
            return try await withCheckedThrowingContinuation { cont in
                self.continuation = cont
                self.startWatchdog()
                manager.requestLocation()
            }
        }
    }

    private func reverseGeocode(_ loc: CLLocation) async throws -> String? {
        let marks = try await CLGeocoder().reverseGeocodeLocation(loc)
        return marks.first?.locality ?? marks.first?.administrativeArea
    }

    nonisolated public func locationManagerDidChangeAuthorization(_ m: CLLocationManager) {
        Task { @MainActor in
            guard self.awaitingAuth else { return }
            switch self.manager.authorizationStatus {
            case .authorizedWhenInUse, .authorizedAlways:
                self.awaitingAuth = false
                self.startWatchdog()
                self.manager.requestLocation()
            case .denied, .restricted:
                self.awaitingAuth = false
                self.watchdog?.cancel()
                self.continuation?.resume(throwing: LocationError.denied); self.continuation = nil
            case .notDetermined:
                break // still waiting for the user's answer
            @unknown default:
                self.awaitingAuth = false
                self.watchdog?.cancel()
                self.continuation?.resume(throwing: LocationError.unavailable); self.continuation = nil
            }
        }
    }

    nonisolated public func locationManager(_ m: CLLocationManager, didUpdateLocations locs: [CLLocation]) {
        guard let loc = locs.last else { return }
        Task { @MainActor in self.watchdog?.cancel(); self.continuation?.resume(returning: loc); self.continuation = nil }
    }
    nonisolated public func locationManager(_ m: CLLocationManager, didFailWithError error: Error) {
        Task { @MainActor in self.watchdog?.cancel(); self.continuation?.resume(throwing: error); self.continuation = nil }
    }
}
