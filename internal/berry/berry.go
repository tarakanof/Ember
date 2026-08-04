// Package berry holds the awtrix-ng Berry scripts Ember installs on the clock.
//
// The scripts are embedded so the binary is self-contained: the container image
// ships no extra files and provisioning never depends on a path on disk. They
// live here rather than under scripts/ because go:embed cannot reach outside
// the embedding package's directory.
package berry

import (
	_ "embed"
	"strings"
)

// BootPingName is the awtrix-ng script name the boot-ping app is installed
// under (PUT /api/v1/apps/script/{name}). Re-PUTting the same name replaces the
// source in place, keeping the app's slot and its store.
const BootPingName = "ember-boot-ping"

//go:embed ember-boot-ping.be
var bootPing string

// bootURLPlaceholder is the token BootPingSource rewrites into the script's
// `# @config url … default=` line.
const bootURLPlaceholder = "__EMBER_BOOT_URL__"

// BootPingSource returns the boot-ping script with url baked in as the default
// of its user-visible `url` setting, so the installed script carries this
// server's own boot-hook URL while staying editable from the device's web UI.
//
// url is interpolated into a double-quoted header field, so it must be a bare
// http(s) URL: a value containing a quote or a newline would corrupt the
// header. Callers pass a URL built from an IP and a port, which cannot.
func BootPingSource(url string) string {
	return strings.ReplaceAll(bootPing, bootURLPlaceholder, url)
}
