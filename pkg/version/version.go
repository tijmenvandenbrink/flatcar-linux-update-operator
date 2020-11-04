// Package version is used to store the version information for the built binaries.
// The Version variable is set by the makefile to the value in the VERSION file
// at the root of the repository.
package version

import (
	"fmt"

	"github.com/blang/semver"
	"k8s.io/klog/v2"
)

var (
	// Version is the semver of this code
	Version = "UNKNOWN"

	// Commit is the git commi tthis was built from
	Commit = "UNKNOWN"
)

var Semver semver.Version

func init() {
	v, err := semver.Parse(Version)
	if err != nil {
		klog.Fatalf("invalid build of update operator; version.Version must be set at compile "+
			"time to a valid semver value. %v could not parse: %v", Version, err)
	}
	Semver = v
}

func Format() string {
	return fmt.Sprintf("Version: %s\nCommit: %s", Version, Commit)
}
