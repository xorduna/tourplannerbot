// Package buildinfo exposes metadata embedded in the application binary at build time.
package buildinfo

// ApplicationVersion is overridden with the immutable image version through Go linker flags.
var ApplicationVersion = "development"

// ApplicationBuildTime is overridden with the UTC image build time through Go linker flags.
var ApplicationBuildTime = "unknown"

// Information contains the build metadata exposed by operational endpoints.
type Information struct {
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
}

// Current returns the metadata embedded in the running application binary.
func Current() Information {
	return Information{
		Version:   ApplicationVersion,
		BuildTime: ApplicationBuildTime,
	}
}
