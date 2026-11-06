package berry

import (
	_ "embed"
	"strings"
)

const BootPingName = "ember-boot-ping"

//go:embed ember-boot-ping.be
var bootPing string

const bootURLPlaceholder = "__EMBER_BOOT_URL__"

func BootPingSource(url string) string {
	return strings.ReplaceAll(bootPing, bootURLPlaceholder, url)
}
