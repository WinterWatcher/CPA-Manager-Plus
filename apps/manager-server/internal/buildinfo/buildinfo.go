// Package buildinfo contains release identity injected by official builds.
package buildinfo

var Version = "dev"
var SourceCommit = "unknown"
var BuildTime = ""

// Info contains release and build metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

// Current returns the active build info.
func Current() Info {
	return Info{
		Version:   Version,
		Commit:    SourceCommit,
		BuildTime: BuildTime,
	}
}
