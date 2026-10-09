package monitor

import (
	"os"
	"testing"
	"time"
)

func TestListsOwnProcess(t *testing.T) {
	self := os.Getpid()
	for _, p := range listProcesses() {
		if p.pid == self {
			if p.ppid != os.Getppid() || p.startTime <= 0 || p.name == "" {
				t.Errorf("got %+v", p)
			}
			return
		}
	}
	t.Fatal("own process not listed")
}

func TestReadsEnvironmentOfOwnProcess(t *testing.T) {
	// 环境变量是进程启动时的快照，只能在子进程里验证；这里只验证读得到且不误报
	if _, ok := hostSessionID(os.Getpid()); ok && os.Getenv("CLAUDE_CODE_HOST_SESSION_ID") == "" {
		t.Error("found a session id that is not in the environment")
	}
}

func TestCPUSamplerMeasuresBusyLoop(t *testing.T) {
	s := newCPUSampler()
	pid := os.Getpid()
	start := time.Now()
	s.sample([]int{pid}, start)
	for time.Since(start) < 200*time.Millisecond {
	}
	usage := s.sample([]int{pid}, time.Now())[pid]
	if usage < 50 {
		t.Errorf("busy loop measured at %.1f%%", usage)
	}
}
