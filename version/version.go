package version

var (
	Version    = "0.1.1"
	SDKVersion = "1.21.0"
)

// VersionInfo returns a formatted version string for diagnostics.
func VersionInfo() string {
	return "must-gather-operator " + Version + " (SDK " + SDKVersion + ")"
}
