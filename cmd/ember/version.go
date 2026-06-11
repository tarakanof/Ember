package main

import "fmt"

// version is the semantic release version, injected at build time via
// -ldflags "-X main.version=X.Y.Z" (see Dockerfile + docker-publish.yml). Local
// and source builds leave it "dev"; the exact commit is always available
// separately as the VCS revision (computeVersionInfo).
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
