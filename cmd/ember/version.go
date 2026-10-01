package main

import "fmt"

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
