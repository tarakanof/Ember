package main

const ngAppEnabledFirmware = "1.2.0"

func clockFirmwareAtLeast(firmware, min string) bool {
	older, ok := versionNewer(min, firmware)
	return ok && !older
}

func clockHasAppEnabled(firmware string) bool {
	return clockFirmwareAtLeast(firmware, ngAppEnabledFirmware)
}
