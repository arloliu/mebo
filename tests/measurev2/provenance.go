package main

import (
	"bufio"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// collectCommon gathers the common metadata of a -profiles invocation:
// the provenance flags, the platform, the build, the runtime environment as this process sees it, and the plan.
func collectCommon(o *options, cells []string) Common {
	affinity, err := cpuAffinity()
	if err != nil {
		logf("Warning: reading the CPU affinity: %v\n", err)
		affinity = []int{}
	}
	configs := make([]DataConfig, 0, len(o.profiles))
	for _, p := range o.profiles {
		configs = append(configs, o.dataConfig(p))
	}

	return Common{
		RunID:         o.runID,
		Source:        o.source,
		Tools:         o.tools,
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
		CPUModel:      cpuModel(),
		GoVersion:     runtime.Version(),
		BuildSettings: buildSettings(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
		CPUAffinity:   affinity,
		GOGC:          os.Getenv("GOGC"),
		GOMEMLIMIT:    os.Getenv("GOMEMLIMIT"),
		GODEBUG:       os.Getenv("GODEBUG"),
		Benchtime:     o.benchtime,
		Rounds:        o.rounds,
		Cells:         o.cells,
		CellsSHA256:   cellsDigest(cells),
		Profiles:      o.profiles,
		DataConfigs:   configs,
	}
}

// buildSettings returns the binary's build settings without the vcs and vcs.* keys,
// which differ between a checkout and a staged copy of the same source.
func buildSettings() []BuildSetting {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return []BuildSetting{}
	}
	settings := make([]BuildSetting, 0, len(info.Settings))
	for _, s := range info.Settings {
		if s.Key != "vcs" && !strings.HasPrefix(s.Key, "vcs.") {
			settings = append(settings, BuildSetting{Key: s.Key, Value: s.Value})
		}
	}

	return settings
}

// cpuModel returns the first "model name" of /proc/cpuinfo, or "unknown" where there is none.
func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(value)
		}
	}

	return "unknown"
}
