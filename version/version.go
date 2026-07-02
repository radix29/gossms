package version

import "runtime"

const Name = "gossms"

var (
	Version = "v0.0.1"
	Commit  = "unknown"
	Date    = "unknown"
)

func Runtime() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
