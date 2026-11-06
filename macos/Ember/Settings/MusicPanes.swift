import SwiftUI
import EmberKit

struct MusicSourcePane: View {
    @Environment(AppEnvironment.self) private var env
    @State private var now: NowPlayingState?
    @State private var loadError: String?

    var body: some View {
        @Bindable var watcher = env.musicWatcher
        Form {
            Section {
                Toggle("Send Apple Music to Ember", isOn: $watcher.enabled)
                if let error = watcher.pusher.lastError, watcher.enabled {
                    Label("Last update didn't reach the server: \(error)", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                }
            } header: {
                Text("Apple Music")
            } footer: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Only on this Mac, while Ember is running. Ember reacts when Music changes track or pauses; it never starts Music and doesn't poll it.")
                    Text("Privacy: the track's title, artist, album, position and artwork go to your Ember server, and anyone on your network can read what's playing from it (no token needed). Artist pictures come from Deezer only if the server has EMBER_ARTIST_LOOKUP=1; then the artist's name leaves your network.")
                    Text("macOS asks once to let Ember control Music. Ember reads the player position, volume and artwork, and runs the knob's controls: play/pause, next, previous and Music's own volume (not the Mac's). Only while this is on and Music is already open; Ember never starts Music.")
                }
            }

            Section {
                if let now, now.isActive {
                    LabeledContent("Track") { Text(verbatim: now.title ?? "") }
                    LabeledContent("Artist") { Text(verbatim: now.artist ?? "") }
                    LabeledContent("Album") { Text(verbatim: now.album ?? "") }
                    LabeledContent("From") { Text(verbatim: now.source ?? "") }
                    LabeledContent("Pictures") {
                        Text(pictures(now))
                    }
                } else if let loadError {
                    Text(verbatim: loadError).foregroundStyle(.secondary)
                } else {
                    Text("Nothing playing.").foregroundStyle(.secondary)
                }
            } header: {
                Text("Now Playing on the Server")
            } footer: {
                Text("Plex is read by the server itself; set it up there (see the Runbook, \"Now playing\").")
            }
        }
        .formStyle(.grouped)
        .task {
            while !Task.isCancelled {
                await refresh()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func pictures(_ s: NowPlayingState) -> LocalizedStringKey {
        switch (s.hasAlbumArt, s.hasArtistArt) {
        case (true, true): "Album and artist"
        case (true, false): "Album"
        case (false, true): "Artist"
        case (false, false): "None"
        }
    }

    private func refresh() async {
        do {
            now = try await env.connection.client.get("/v1/nowplaying/state")
            loadError = nil
        } catch {
            now = nil
            loadError = error.localizedDescription
        }
    }
}
