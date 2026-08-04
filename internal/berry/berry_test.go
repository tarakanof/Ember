package berry

import "strings"

import "testing"

func TestBootPingSourceBakesCallbackURL(t *testing.T) {
	src := BootPingSource("http://192.168.0.2:3627/hooks/awtrix/boot")
	if strings.Contains(src, "__EMBER_BOOT_URL__") {
		t.Fatalf("placeholder survived substitution:\n%s", src)
	}
	if !strings.Contains(src, `default="http://192.168.0.2:3627/hooks/awtrix/boot"`) {
		t.Fatalf("callback URL not baked into the @config default:\n%s", src)
	}
}

// The header lines are what the device's web UI reads, and the trailing
// `return` is what makes the file an app at all — an install without it
// compiles and then never runs.
func TestBootPingSourceHasAppShape(t *testing.T) {
	src := BootPingSource("http://e/hooks/awtrix/boot")
	for _, want := range []string{
		"# @name    " + BootPingName,
		"# @headless true",
		"# @config  url text ",
		"class EmberBootPing",
		"def loop()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(src), "return EmberBootPing()") {
		t.Errorf("script must end with the instance return, got tail %q",
			strings.TrimSpace(src)[max(0, len(strings.TrimSpace(src))-40):])
	}
}

// The 8 KB script cap is the device's, and the compile — not the source — is
// the binding constraint; a script this small must stay small.
func TestBootPingSourceFitsScriptBudget(t *testing.T) {
	if n := len(BootPingSource("http://192.168.0.2:3627/hooks/awtrix/boot")); n > 8192 {
		t.Fatalf("script is %d bytes, over the device's 8192-byte scriptMaxBytes", n)
	}
}
