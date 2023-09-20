package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	GitCommit            string
	GitBranch            string
	GitSummary           string
	BuildDate            string
	AppVersion           string
	ServerserviceVersion = serverserviceVersion()
	GoVersion            = runtime.Version()
)

type Version struct {
	GitCommit            string `json:"git_commit"`
	GitBranch            string `json:"git_branch"`
	GitSummary           string `json:"git_summary"`
	BuildDate            string `json:"build_date"`
	AppVersion           string `json:"app_version"`
	GoVersion            string `json:"go_version"`
	ServerserviceVersion string `json:"serverservice_version"`
}

func Current() *Version {
	return &Version{
		GitBranch:            GitBranch,
		GitCommit:            GitCommit,
		GitSummary:           GitSummary,
		BuildDate:            BuildDate,
		AppVersion:           AppVersion,
		GoVersion:            GoVersion,
		ServerserviceVersion: ServerserviceVersion,
	}
}

func (v *Version) String() string {
	return fmt.Sprintf("version=%s ref=%s branch=%s built=%s", v.AppVersion, v.GitCommit, v.GitBranch, v.BuildDate)
}

func serverserviceVersion() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	for _, d := range buildInfo.Deps {
		if strings.Contains(d.Path, "serverservice") {
			return d.Version
		}
	}

	return ""
}
