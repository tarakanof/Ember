package producer

import (
	"strings"
	"testing"
)

func TestEnvExample_HasRequiredKeys(t *testing.T) {
	s := EnvExample()
	for _, k := range []string{"EMBER_SOURCE=", "EMBER_SERVER_URL=", "EMBER_TOKEN="} {
		if !strings.Contains(s, k) {
			t.Errorf("EnvExample() missing %q", k)
		}
	}
}

func TestEnvExample_DefaultsToServerDiscovery(t *testing.T) {
	if !strings.Contains(EnvExample(), "\nEMBER_SERVER_URL=\n") {
		t.Errorf("EnvExample() should seed an empty (discovered) EMBER_SERVER_URL:\n%s", EnvExample())
	}
}
