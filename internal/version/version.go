package version

// Version is the current phi release shown on the splash screen and used by
// `phi update`. Override at build time with:
//
//	go build -ldflags="-X github.com/pulseaiclub/phi/internal/version.Version=v0.2.0"
var Version = "v0.26.0"
