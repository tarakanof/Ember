// Package berry holds the awtrix-ng Berry scripts Ember installs on the clock.
package berry

import (
	_ "embed"
	"strings"
)

// BootPingName is the awtrix-ng script name the boot-ping app is installed
// under (PUT /api/v1/apps/script/{name}).
const BootPingName = "ember-boot-ping"

//go:embed ember-boot-ping.be
var bootPing string

const bootURLPlaceholder = "__EMBER_BOOT_URL__"

// BootPingSource returns the boot-ping script with url baked in as the default
// of its user-visible `url` setting, so the installed script carries this
// server's own boot-hook URL while staying editable from the device's web UI.
func BootPingSource(url string) string {
	return strings.ReplaceAll(bootPing, bootURLPlaceholder, url)
}
