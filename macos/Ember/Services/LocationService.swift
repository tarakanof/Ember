import CoreLocation
import MapKit
import Observation

@MainActor
@Observable
public final class LocationService: NSObject, CLLocationManagerDelegate {
    public struct Fix: Sendable { public let latitude: Double; public let longitude: Double; public let name: String? }
    public enum LocationError: Error {
        case denied
        case unavailable
        case authorizationUnavailable
    }

    @ObservationIgnored private let manager = CLLocationManager()
    @ObservationIgnored private var continuation: CheckedContinuation<CLLocation, Error>?
    @ObservationIgnored private var awaitingAuth = false
    @ObservationIgnored private var watchdog: Task<Void, Never>?

    public private(set) var authStatus: CLAuthorizationStatus = .notDetermined

    public override init() {
        super.init()
        manager.delegate = self
        manager.desiredAccuracy = kCLLocationAccuracyKilometer
        authStatus = manager.authorizationStatus
    }

    public func refreshAuthorization() {
        authStatus = manager.authorizationStatus
    }

    public func current() async throws -> Fix {
        let loc = try await requestFix()
        let name = try? await reverseGeocode(loc)
        return Fix(latitude: loc.coordinate.latitude, longitude: loc.coordinate.longitude, name: name)
    }

    private func startWatchdog() {
        watchdog?.cancel()
        watchdog = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(20))
            guard let self, let cont = self.continuation else { return }
            let wasAwaitingAuth = self.awaitingAuth
            self.continuation = nil
            self.awaitingAuth = false
            cont.resume(throwing: wasAwaitingAuth ? LocationError.authorizationUnavailable : LocationError.unavailable)
        }
    }

    private func requestFix() async throws -> CLLocation {
        guard continuation == nil else { throw LocationError.unavailable }
        switch manager.authorizationStatus {
        case .denied, .restricted:
            throw LocationError.denied
        case .notDetermined:
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
        guard let request = MKReverseGeocodingRequest(location: loc) else { return nil }
        let address = try await request.mapItems.first?.addressRepresentations
        return address?.cityName ?? address?.cityWithContext
    }

    nonisolated public func locationManagerDidChangeAuthorization(_ m: CLLocationManager) {
        Task { @MainActor in
            self.authStatus = self.manager.authorizationStatus
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
                break
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
