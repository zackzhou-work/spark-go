package monitor

import (
	"bytes"
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

var hostSessionKey = []byte("CLAUDE_CODE_HOST_SESSION_ID=")

// hostSessionID 读 Claude 核心进程环境变量里的宿主会话 ID，与桌面端会话 JSON 的 sessionId 一一对应。
// KERN_PROCARGS2 返回 argc、可执行路径、参数与环境变量，以 NUL 分隔
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

// cpuSampler 按两次扫描之间的 CPU 时间增量算占用率，口径和 sysinfo 一致：单核百分比
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

// sample 只采给定的这些进程，其余的旧样本顺手丢掉
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

// 进程累计 CPU 时间没有 sysctl 可读，只能走 libproc 的 proc_pid_rusage；用 purego 调，保持不开 cgo
var (
	libprocOnce   sync.Once
	procPidRusage func(pid int32, flavor int32, buf unsafe.Pointer) int32
	machTimebase  func(info unsafe.Pointer) int32
	tickNumer     uint64 = 1
	tickDenom     uint64 = 1
)

// rusage_info_v0 的前几个字段，后面的用不上
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
	// Apple Silicon 上 rusage 的时间单位是 mach tick，不是纳秒
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
