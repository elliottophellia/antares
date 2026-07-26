// Package version carries build identity for Antares.
package version

// Values are overridable at build time with -ldflags.
var (
	Name    = "antares"
	Display = "Antares"
	Version = "0.1.0-dev"
	Commit  = "dev"
	Date    = "unknown"
)

// UserAgent is sent on every outbound HTTP request Antares makes.
func UserAgent() string {
	return Display + "/" + Version
}
