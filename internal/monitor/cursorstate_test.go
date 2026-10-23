package monitor

import (
	"testing"
	"time"
)

const testConv = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"

// event builds one collector event; at is when this machine received it
func event(at time.Time, generation, name string) CursorEvent {
	return CursorEvent{
		Schema:         cursorEventSchema,
		ReceivedAtMs:   at.UnixMilli(),
		ConversationID: testConv,
		GenerationID:   generation,
		Event:          name,
	}
}

func stopEvent(at time.Time, generation, status string) CursorEvent {
	ev := event(at, generation, "stop")
	ev.Status = status
	return ev
}

func endEvent(at time.Time, reason string) CursorEvent {
	ev := event(at, "", "sessionEnd")
	ev.Reason = reason
	return ev
}

func headerAt(updated time.Time, unread *bool) *cursorHeader {
	return &cursorHeader{ID: testConv, Title: "t", UpdatedAt: updated, Unread: unread, LocalDesktop: true}
}

func boolPtr(b bool) *bool { return &b }

var stateNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)

func stateOf(e cursorSessionEvidence) TaskState {
	return decideCursorVerdict(e, stateNow).State
}

// A submitted prompt is a turn starting, not a turn that finished: it may be held as running
// only for as long as a submission plausibly takes to show activity
func TestSubmittedPromptIsRunningOnlyWhileFresh(t *testing.T) {
	submitted := event(stateNow.Add(-time.Second), "g1", "beforeSubmitPrompt")
	if got := stateOf(cursorSessionEvidence{Events: []CursorEvent{submitted}}); got != Running {
		t.Errorf("a prompt submitted a second ago should be running, got %v", got)
	}
	old := event(stateNow.Add(-2*cursorSubmitGrace), "g1", "beforeSubmitPrompt")
	if got := stateOf(cursorSessionEvidence{Events: []CursorEvent{old}}); got != Unknown {
		t.Errorf("a submission with no activity after the grace should be unknown, got %v", got)
	}
}

// afterAgentResponse and afterAgentThought mean the agent is working, not that the turn ended
func TestActivityKeepsATurnRunningUntilItGoesStale(t *testing.T) {
	for _, name := range []string{"postToolUse", "postToolUseFailure", "afterAgentThought", "afterAgentResponse"} {
		submitted := event(stateNow.Add(-time.Minute), "g1", "beforeSubmitPrompt")
		active := event(stateNow.Add(-5*time.Second), "g1", name)
		if got := stateOf(cursorSessionEvidence{Events: []CursorEvent{submitted, active}}); got != Running {
			t.Errorf("%s: want running, got %v", name, got)
		}
		// Past the activity window nothing can be said, and stale is not finished
		stale := event(stateNow.Add(-2*cursorActivityTTL), "g1", name)
		if got := stateOf(cursorSessionEvidence{Events: []CursorEvent{submitted, stale}}); got != Unknown {
			t.Errorf("%s: want unknown once activity is stale, got %v", name, got)
		}
	}
}

// The end of a turn says nothing about whether the result was looked at, so the read state has
// to come from metadata read after that end. Cursor's own header timestamps are stamped when a
// turn starts and when its last content lands, so neither can be compared with the stop
func TestEndedTurnIsUnreadOrCompletedOnlyFromMetadataReadAfterwards(t *testing.T) {
	ended := stopEvent(stateNow.Add(-time.Minute), "g1", "completed")
	readAfter := stateNow.Add(-time.Minute + cursorMetadataSettle + time.Second)
	for _, tc := range []struct {
		name   string
		header *cursorHeader
		readAt time.Time
		want   TaskState
	}{
		{"unread", headerAt(stateNow.Add(-2*time.Minute), boolPtr(true)), readAfter, Unread},
		{"read", headerAt(stateNow.Add(-2*time.Minute), boolPtr(false)), readAfter, Completed},
		// A read from before the turn ended can't know about this turn
		{"read before the end", headerAt(stateNow.Add(-time.Minute), boolPtr(false)), stateNow.Add(-2 * time.Minute), Unknown},
		// Nor can one taken straight after it, before Cursor has settled
		{"read during the settle", headerAt(stateNow.Add(-time.Minute), boolPtr(false)), stateNow.Add(-time.Minute + 10*time.Millisecond), Unknown},
		{"no metadata", nil, readAfter, Unknown},
		{"no unread field", headerAt(stateNow.Add(-time.Minute), nil), readAfter, Unknown},
	} {
		got := decideCursorVerdict(cursorSessionEvidence{Header: tc.header, HeaderReadAt: tc.readAt, Events: []CursorEvent{ended}}, stateNow)
		if got.State != tc.want {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got.State)
		}
	}
}

// A cancelled or failed turn still ended; the reason is kept internally, and neither is shown
// as success
func TestAbortedAndFailedTurnsEndTheTurn(t *testing.T) {
	for _, status := range []string{"aborted", "error"} {
		ended := stopEvent(stateNow.Add(-time.Minute), "g1", status)
		got := decideCursorVerdict(cursorSessionEvidence{Header: headerAt(stateNow.Add(-time.Second), boolPtr(false)), HeaderReadAt: stateNow, Events: []CursorEvent{ended}}, stateNow)
		if got.State != Completed {
			t.Errorf("%s: want completed, got %v", status, got.State)
		}
		if got.Terminal != status {
			t.Errorf("%s: the terminal reason should be kept, got %q", status, got.Terminal)
		}
	}
}

// Without any event, a session is only as clear as the metadata makes it
func TestMetadataAloneCannotClaimATurnFinished(t *testing.T) {
	if got := stateOf(cursorSessionEvidence{Header: headerAt(stateNow.Add(-time.Hour), boolPtr(true))}); got != Unread {
		t.Errorf("an unread session should show as unread, got %v", got)
	}
	if got := stateOf(cursorSessionEvidence{Header: headerAt(stateNow.Add(-time.Hour), boolPtr(false))}); got != Unknown {
		t.Errorf("a read session with no events says nothing about running, got %v", got)
	}
	if got := stateOf(cursorSessionEvidence{}); got != Unknown {
		t.Errorf("no evidence should be unknown, got %v", got)
	}
}

// Cursor reports a generation per user message, so an old turn's ending must not close a new
// one
func TestAnOldGenerationsStopDoesNotEndTheNewTurn(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-4*time.Minute), "g1", "beforeSubmitPrompt"),
		stopEvent(stateNow.Add(-3*time.Minute), "g1", "completed"),
		event(stateNow.Add(-2*time.Minute), "g2", "beforeSubmitPrompt"),
		event(stateNow.Add(-10*time.Second), "g2", "postToolUse"),
	}
	if got := stateOf(cursorSessionEvidence{Header: headerAt(stateNow.Add(-30*time.Second), boolPtr(false)), Events: events}); got != Running {
		t.Errorf("want running, got %v", got)
	}
}

// A stop without a generation can't be matched to the turn it belongs to
func TestAGenerationlessStopCannotEndAGeneration(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-2*time.Minute), "g1", "beforeSubmitPrompt"),
		event(stateNow.Add(-10*time.Second), "g1", "postToolUse"),
		stopEvent(stateNow.Add(-time.Minute), "", "completed"),
	}
	if got := stateOf(cursorSessionEvidence{Header: headerAt(stateNow.Add(-time.Second), boolPtr(false)), Events: events}); got != Running {
		t.Errorf("want running, got %v", got)
	}
}

// An event after the end of its own turn could be a late hook or a real continuation, and this
// machine has no sequence to tell them apart
func TestActivityAfterItsTurnsEndIsUnknown(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-3*time.Minute), "g1", "beforeSubmitPrompt"),
		stopEvent(stateNow.Add(-2*time.Minute), "g1", "completed"),
		event(stateNow.Add(-10*time.Second), "g1", "postToolUse"),
	}
	if got := stateOf(cursorSessionEvidence{Header: headerAt(stateNow.Add(-time.Second), boolPtr(false)), Events: events}); got != Unknown {
		t.Errorf("want unknown, got %v", got)
	}
}

// A follow-up submit is a new user message, so the new generation may run after the last one
// stopped
func TestANewGenerationRunsAfterTheOldOneStopped(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-3*time.Minute), "g1", "beforeSubmitPrompt"),
		stopEvent(stateNow.Add(-2*time.Minute), "g1", "completed"),
		event(stateNow.Add(-time.Minute), "g2", "beforeSubmitPrompt"),
		event(stateNow.Add(-5*time.Second), "g2", "afterAgentThought"),
	}
	if got := stateOf(cursorSessionEvidence{Header: headerAt(stateNow.Add(-10*time.Second), boolPtr(false)), Events: events}); got != Running {
		t.Errorf("want running, got %v", got)
	}
}

func TestRepeatedEventsDoNotChangeTheAnswer(t *testing.T) {
	ended := stopEvent(stateNow.Add(-time.Minute), "g1", "completed")
	header := headerAt(stateNow.Add(-30*time.Second), boolPtr(true))
	want := stateOf(cursorSessionEvidence{Header: header, Events: []CursorEvent{ended}})
	duplicated := []CursorEvent{ended, ended, ended}
	if got := stateOf(cursorSessionEvidence{Header: header, Events: duplicated}); got != want {
		t.Errorf("duplicates changed the answer: %v vs %v", got, want)
	}
	// Two different endings for one turn contradict each other
	conflict := []CursorEvent{ended, stopEvent(stateNow.Add(-time.Minute), "g1", "error")}
	if got := stateOf(cursorSessionEvidence{Header: header, Events: conflict}); got != Unknown {
		t.Errorf("contradicting endings should be unknown, got %v", got)
	}
}

// Same-millisecond evidence can't be ordered, and the event id is not a sequence
func TestConflictingEvidenceAtTheSameInstantIsUnknown(t *testing.T) {
	at := stateNow.Add(-time.Minute)
	events := []CursorEvent{
		event(at, "g1", "beforeSubmitPrompt"),
		stopEvent(at, "g1", "completed"),
		event(at, "g1", "postToolUse"),
	}
	if got := stateOf(cursorSessionEvidence{Header: headerAt(at, boolPtr(false)), Events: events}); got != Unknown {
		t.Errorf("want unknown, got %v", got)
	}
}

// Two generations working at once, with no submission to say which is current, is a conflict
func TestTwoLiveGenerationsWithoutASubmissionAreUnknown(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-2*time.Minute), "g1", "postToolUse"),
		event(stateNow.Add(-time.Minute), "g2", "postToolUse"),
	}
	if got := stateOf(cursorSessionEvidence{Events: events}); got != Unknown {
		t.Errorf("want unknown, got %v", got)
	}
}

// A generation with no submission can't take a turn away from one that has it
func TestAGenerationWithoutASubmissionCannotTakeOver(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-2*time.Minute), "g1", "beforeSubmitPrompt"),
		event(stateNow.Add(-time.Minute), "g2", "postToolUse"),
	}
	if got := stateOf(cursorSessionEvidence{Events: events}); got != Running {
		t.Errorf("want the submitted turn to stay running, got %v", got)
	}
}

// sessionEnd ends the session, not the machine: it is terminal evidence for the session's own
// turn
func TestSessionEndEndsTheSessionTurn(t *testing.T) {
	events := []CursorEvent{
		event(stateNow.Add(-5*time.Minute), "g1", "beforeSubmitPrompt"),
		event(stateNow.Add(-4*time.Minute), "g1", "postToolUse"),
		endEvent(stateNow.Add(-time.Minute), "window_close"),
	}
	got := decideCursorVerdict(cursorSessionEvidence{Header: headerAt(stateNow.Add(-30*time.Second), boolPtr(false)), HeaderReadAt: stateNow, Events: events}, stateNow)
	if got.State != Completed {
		t.Errorf("want completed, got %v", got.State)
	}
	if got.Terminal != "window_close" {
		t.Errorf("the end reason should be kept, got %q", got.Terminal)
	}
	// Activity in a new generation after the session ended brings it back, which is what a
	// reopened conversation looks like
	events = append(events, event(stateNow.Add(-10*time.Second), "g2", "beforeSubmitPrompt"))
	if got := decideCursorVerdict(cursorSessionEvidence{Header: headerAt(stateNow.Add(-5*time.Second), boolPtr(false)), HeaderReadAt: stateNow, Events: events}, stateNow); got.State != Running {
		t.Errorf("want running after a new turn, got %v", got.State)
	}
}

// A terminal whose metadata was read before it arrived is not a licence to guess; the next scan
// reads the metadata again and can then say
func TestTerminalWithoutCorrelatedMetadataStaysUnknownUntilMetadataIsReread(t *testing.T) {
	ended := stopEvent(stateNow.Add(-time.Minute), "g1", "completed")
	header := headerAt(stateNow.Add(-5*time.Minute), boolPtr(false))
	if got := stateOf(cursorSessionEvidence{Header: header, HeaderReadAt: stateNow.Add(-2 * time.Minute), Events: []CursorEvent{ended}}); got != Unknown {
		t.Fatalf("want unknown while the metadata predates the stop, got %v", got)
	}
	if got := stateOf(cursorSessionEvidence{Header: header, HeaderReadAt: stateNow, Events: []CursorEvent{ended}}); got != Completed {
		t.Errorf("want completed once the metadata is read after the stop, got %v", got)
	}
}
