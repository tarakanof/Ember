import SwiftUI

extension View {
    public func holdsLive(_ feed: Feed, in live: LiveModel, while active: Bool) -> some View {
        task(id: active) {
            guard active else { return }
            await live.track(feed)
        }
    }
}
