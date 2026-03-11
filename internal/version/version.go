package version

import (
	"runtime"
	"time"
)

// Set via ldflags at build time:
//
//	go build -ldflags "-X github.com/CTJaeger/KleverNodeHub/internal/version.Version=v0.1.0
//	  -X github.com/CTJaeger/KleverNodeHub/internal/version.GitCommit=abc1234
//	  -X github.com/CTJaeger/KleverNodeHub/internal/version.BuildTime=2026-03-11T17:00:00Z"
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// Info holds structured build information.
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get returns the current build information.
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// StartTime records when the process started.
var StartTime = time.Now()

// Uptime returns how long the process has been running.
func Uptime() time.Duration {
	return time.Since(StartTime)
}
