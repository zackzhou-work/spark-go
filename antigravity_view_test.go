package main

import (
	"testing"
	"time"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

func TestAntigravityWaitingCardAndRowJump(t *testing.T) {
	tempHome(t)
	now := time.Now()

	a := &app{groups: []monitor.ProjectGroup{
		{
			ProjectName: "spark-go",
			Sessions: []monitor.SessionItem{
				{
					ID:         "11111111-1111-4111-8111-111111111111",
					Source:     monitor.SourceAntigravity,
					Title:      "Choose target DB",
					State:      monitor.Waiting,
					WaitReason: monitor.WaitQuestion,
					WorkingDir: "/Users/someone/spark-go",
					ActivityAt: now.Add(-1 * time.Minute),
					IsToday:    true,
				},
				{
					ID:         "22222222-2222-4222-8222-222222222222",
					Source:     monitor.SourceAntigravity,
					Title:      "Finish scan refactor",
					State:      monitor.Completed,
					WorkingDir: "/Users/someone/spark-go",
					ActivityAt: now.Add(-5 * time.Minute),
					IsToday:    true,
				},
			},
		},
	}}

	tt := newViewTester(a, 360, 640)
	if !tt.HasText("Answer") {
		t.Fatalf("expected Today tile waiting action; got %q", tt.Texts())
	}
	if tt.HasText("Install for Antigravity") {
		t.Fatal("Antigravity integration should not show a hook install prompt")
	}

	if err := tt.Click("Answer"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != "antigravity://focus" {
		t.Fatalf("Today waiting action opened %v; want [antigravity://focus]", got)
	}

	// Open the project page: waiting card shows "Answer in Antigravity" and completed row also jumps.
	if err := tt.Click("spark-go"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Answer in Antigravity") {
		t.Fatalf("expected project waiting card label %q; got %q", "Answer in Antigravity", tt.Texts())
	}
	if err := tt.Click("Finish scan refactor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.OpenedURLs(); len(got) != 2 || got[1] != "antigravity://focus" {
		t.Fatalf("row click opened %v; want second URL antigravity://focus", got)
	}
}
