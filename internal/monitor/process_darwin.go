package monitor

import (
	"bytes"
	"errors"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

type procInfo struct {
	pid       int
	ppid      int
	startTime int64
	name      string
}

func listProcesses() []procInfo {
	kinfos, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	out := make([]procInfo, 0, len(kinfos))
	for i := range kinfos {
		p := &kinfos[i].Proc
		name := p.P_comm[:]
		if n := bytes.IndexByte(name, 0); n >= 0 {
			name = name[:n]
		}
		out = append(out, procInfo{
			pid:       int(p.P_pid),
			ppid:      int(kinfos[i].Eproc.Ppid),
			startTime: p.P_starttime.Sec,
			name:      string(name),
		})
	}
	return out
}

// processAlive reports whether a pid is still in use. Not being allowed to signal another
// user's process still proves it exists
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

var hostSessionKey = []byte("CLAUDE_CODE_HOST_SESSION_ID=")

// hostSessionID reads the host session ID from a claude core process's environment, which
// maps one-to-one onto the sessionId in the desktop session JSON. KERN_PROCARGS2 returns
// argc, the executable path, arguments and environment, NUL separated
func hostSessionID(pid int) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	for _, field := range bytes.Split(buf, []byte{0}) {
		if bytes.HasPrefix(field, hostSessionKey) {
			return string(field[len(hostSessionKey):]), true
		}
	}
	return "", false
}

// cpuSampler derives usage from the CPU time delta between two scans, matching sysinfo:
// a percentage of a single core
type cpuSampler struct {
	prev map[int]cpuSample
}

type cpuSample struct {
	cpuNs int64
	at    time.Time
}

func newCPUSampler() *cpuSampler {
	return &cpuSampler{prev: map[int]cpuSample{}}
}

// sample only samples the given processes and drops every other previous sample
func (s *cpuSampler) sample(pids []int, now time.Time) map[int]float32 {
	usage := make(map[int]float32, len(pids))
	next := make(map[int]cpuSample, len(pids))
	for _, pid := range pids {
		ns, ok := processCPUTimeNs(pid)
		if !ok {
			continue
		}
		cur := cpuSample{cpuNs: ns, at: now}
		next[pid] = cur
		if prev, ok := s.prev[pid]; ok {
			if wall := cur.at.Sub(prev.at).Nanoseconds(); wall > 0 {
				usage[pid] = float32(cur.cpuNs-prev.cpuNs) / float32(wall) * 100
			}
		}
	}
	s.prev = next
	return usage
}

// No sysctl exposes a process's cumulative CPU time, so this goes through libproc's
// proc_pid_rusage via purego, keeping cgo off
var (
	libprocOnce   sync.Once
	procPidRusage func(pid int32, flavor int32, buf unsafe.Pointer) int32
	machTimebase  func(info unsafe.Pointer) int32
	tickNumer     uint64 = 1
	tickDenom     uint64 = 1
)

// The first few fields of rusage_info_v0; the rest are unused
type rusageInfoV0 struct {
	uuid       [16]byte
	userTime   uint64
	systemTime uint64
	_          [8]uint64
}

func loadLibproc() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&procPidRusage, lib, "proc_pid_rusage")
	purego.RegisterLibFunc(&machTimebase, lib, "mach_timebase_info")
	// On Apple Silicon rusage reports time in mach ticks, not nanoseconds
	var tb struct{ numer, denom uint32 }
	if machTimebase(unsafe.Pointer(&tb)) == 0 && tb.denom != 0 {
		tickNumer, tickDenom = uint64(tb.numer), uint64(tb.denom)
	}
}

func processCPUTimeNs(pid int) (int64, bool) {
	libprocOnce.Do(loadLibproc)
	if procPidRusage == nil {
		return 0, false
	}
	var info rusageInfoV0
	if procPidRusage(int32(pid), 0, unsafe.Pointer(&info)) != 0 {
		return 0, false
	}
	ticks := info.userTime + info.systemTime
	return int64(ticks * tickNumer / tickDenom), true
}
