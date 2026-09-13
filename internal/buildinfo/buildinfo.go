package buildinfo

// These values are replaced at build time through -ldflags. The defaults make
// local development builds explicit and reproducible.
var (
	Version = "0.31.1"
	Commit = "unknown"
	BuildTime = "unknown"
)

const (
	ProductName = "control-center"
	APIVersion = "v1"
)
