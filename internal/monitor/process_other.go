//go:build !darwin

package monitor

import "time"

type procInfo struct {
	pid       int
	ppid      int
	startTime int64
	name      string
}

func listProcesses() []procInfo { return nil }

func hostSessionID(int) (string, bool) { return "", false }

type cpuSampler struct{}

func newCPUSampler() *cpuSampler { return &cpuSampler{} }

func (*cpuSampler) sample([]int, time.Time) map[int]float32 { return nil }
