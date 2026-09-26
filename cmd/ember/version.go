package main

import "fmt"

// version is the semantic release version, injected at build time via
// -ldflags "-X main.version=X.Y.Z" (see Dockerfile + docker-publish.yml). Local
// and source builds leave it "dev"; the exact commit is always available
// separately as the VCS revision (computeVersionInfo). Keep it in package main:
// version_ldflags_test.go builds with the Dockerfile's ldflags and fails if the
// -X target stops resolving (the linker ignores unresolved targets silently).
var version = "dev"

func runVersion() {
	v := computeVersionInfo()
	rev := v.Revision
	if rev == "" {
		rev = "unknown"
	}
	if v.Dirty {
		rev += "+dirty"
	}
	fmt.Printf("ember %s (%s, %s)\n", v.Version, rev, v.GoVersion)
}
