// Package version reports which build of tui-builder / wrangl is running.
package version

import "runtime/debug"

// Version is stamped at release time by goreleaser:
//
//	-ldflags "-X github.com/jsdrews/tui-builder/internal/version.Version=v1.2.3"
var Version = ""

// String returns the stamped version, or the module version Go records
// for a `go install …@vX` build, or "dev" for a local build.
func String() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
