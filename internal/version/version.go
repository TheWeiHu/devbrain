// Package version holds the build-stamped version identity.
// Release builds inject these via -ldflags "-X .../internal/version.Version=…".
package version

var (
	Version = "dev"
	Commit  = ""
)

// String is what `devbrain version` prints.
func String() string {
	if Commit != "" && Version == "dev" {
		return Version + " (" + Commit + ")"
	}
	return Version
}
