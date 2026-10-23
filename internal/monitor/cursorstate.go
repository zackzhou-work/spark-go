package monitor

import "time"

const (
	// cursorActivityTTL is how long observed activity stays believable. Past it a quiet session
	// is unknown, not finished: a long tool call can outlast any window spark picks
	cursorActivityTTL = 30 * time.Minute
	// cursorSubmitGrace covers a submission with no activity behind it yet. Another hook can
	// refuse that submission, so this is a short window rather than a state
	cursorSubmitGrace = 60 * time.Second
	// cursorMetadataSettle is how long after a turn ends the metadata is left to catch up. The
	// header's own timestamps are written when a turn starts and when its last content lands, so
	// neither can be compared with the stop: what is compared is when the metadata was read
	cursorMetadataSettle = 2 * time.Second
)

// cursorVerdict is what the evidence adds up to, plus the reason it says so, which is logged when
// a session's state moves. Terminal is
// Cursor's own word (completed, aborted, error, window_close, user_close) and is never shown as
// success: spark's Completed means ended and read
type cursorVerdict struct {
	State    TaskState
	Terminal string
	Reason   string
}

// cursorSessionEvidence is one conversation as the reducer sees it: the metadata that was read
// just now, and whatever hook events are inside the retention window
type cursorSessionEvidence struct {
	Header *cursorHeader
	// HeaderReadAt is when that metadata was read off disk. Cursor writes the read flag around a
	// turn, and the moment spark looked is what says whether the look came after the turn ended
	HeaderReadAt time.Time
	Events       []CursorEvent
}

// cursorTurn is one generation of one conversation. Cursor reports a new generation ID for each
// user message, and IDs are opaque: they are never sorted
type cursorTurn struct {
	generation     string
	submittedAt    int64
	lastActivityAt int64
	// stops and ends are the terminal reports seen, keyed by status or reason, holding the
	// earliest arrival of each. Two different values are a contradiction
	stops map[string]int64
	ends  map[string]int64
}

func newCursorTurn(generation string) *cursorTurn {
	return &cursorTurn{generation: generation, stops: map[string]int64{}, ends: map[string]int64{}}
}

func (t *cursorTurn) latestEvidenceAt() int64 {
	return max(t.submittedAt, t.lastActivityAt)
}

// terminalAt is the earliest ending reported for this turn, and what it said
func (t *cursorTurn) terminalAt() (int64, string) {
	at, word := int64(0), ""
	if t == nil {
		return 0, ""
	}
	for status, when := range t.stops {
		if at == 0 || when < at {
			at, word = when, status
		}
	}
	for reason, when := range t.ends {
		if at == 0 || when < at {
			at, word = when, reason
		}
	}
	return at, word
}

// decideCursorVerdict turns events and metadata into a state, saying Unknown whenever the two
// can't be tied together
func decideCursorVerdict(e cursorSessionEvidence, now time.Time) cursorVerdict {
	turns := collapseCursorTurns(e.Events)
	current, ambiguous := pickCursorTurn(turns, now)
	if ambiguous {
		return cursorVerdict{State: Unknown, Reason: "more than one turn could be current"}
	}
	if current == nil {
		// Nothing was observed: the metadata is all there is, and only unread is provable
		if e.Header != nil && e.Header.Unread != nil && *e.Header.Unread {
			return cursorVerdict{State: Unread, Reason: "metadata says unread"}
		}
		return cursorVerdict{State: Unknown, Reason: "no turn evidence"}
	}

	// A session end closes whatever turn was running, unless a new turn started after it
	if current.generation != "" {
		if session := turns[""]; session != nil {
			// The end closes the session, so it counts unless something was used after it
			if endAt, reason := session.terminalAt(); endAt != 0 && endAt >= latestLiveEvidence(turns) {
				return finishedCursorVerdict(e.Header, e.HeaderReadAt, endAt, reason)
			}
		}
	}

	if len(current.stops) > 1 || len(current.ends) > 1 {
		return cursorVerdict{State: Unknown, Reason: "contradicting endings for one turn"}
	}
	terminalAt, word := current.terminalAt()
	if terminalAt != 0 {
		if current.lastActivityAt >= terminalAt {
			// A late hook and a real continuation look identical from here
			return cursorVerdict{State: Unknown, Terminal: word, Reason: "activity after the turn ended"}
		}
		return finishedCursorVerdict(e.Header, e.HeaderReadAt, terminalAt, word)
	}

	switch {
	case current.lastActivityAt != 0:
		if now.UnixMilli()-current.lastActivityAt <= cursorActivityTTL.Milliseconds() {
			return cursorVerdict{State: Running, Reason: "recent activity"}
		}
		return cursorVerdict{State: Unknown, Reason: "activity went stale"}
	case current.submittedAt != 0:
		if now.UnixMilli()-current.submittedAt <= cursorSubmitGrace.Milliseconds() {
			return cursorVerdict{State: Running, Reason: "just submitted"}
		}
		return cursorVerdict{State: Unknown, Reason: "submitted with no activity"}
	}
	return cursorVerdict{State: Unknown, Reason: "no turn evidence"}
}

// finishedCursorVerdict splits an ended turn into unread and read, and refuses to unless the
// metadata was written after the end
func finishedCursorVerdict(header *cursorHeader, readAt time.Time, terminalAt int64, word string) cursorVerdict {
	unread, ok := correlatedCursorUnread(header, readAt, terminalAt)
	switch {
	case !ok:
		return cursorVerdict{State: Unknown, Terminal: word, Reason: "no metadata from after the turn ended"}
	case *unread:
		return cursorVerdict{State: Unread, Terminal: word, Reason: "ended and still unread"}
	default:
		return cursorVerdict{State: Completed, Terminal: word, Reason: "ended and read"}
	}
}

// correlatedCursorUnread answers with the metadata's read state only when that metadata was read
// after the turn ended, with a moment for Cursor to settle. A read from before the turn finished
// says nothing about how the turn ended, whatever its timestamp
func correlatedCursorUnread(header *cursorHeader, readAt time.Time, terminalAt int64) (*bool, bool) {
	if header == nil || header.Unread == nil || readAt.IsZero() {
		return nil, false
	}
	if readAt.UnixMilli() < terminalAt+int64(cursorMetadataSettle/time.Millisecond) {
		return nil, false
	}
	return header.Unread, true
}

// collapseCursorTurns groups events by generation, keeping the earliest submission, the latest
// activity and the earliest of each terminal report. Received times are all this machine has,
// so nothing here orders events within one generation
func collapseCursorTurns(events []CursorEvent) map[string]*cursorTurn {
	turns := map[string]*cursorTurn{}
	if len(events) == 0 {
		return turns
	}
	for _, ev := range events {
		if ev.ConversationID == "" || ev.ReceivedAtMs <= 0 {
			continue
		}
		turn, ok := turns[ev.GenerationID]
		if !ok {
			turn = newCursorTurn(ev.GenerationID)
			turns[ev.GenerationID] = turn
		}
		switch ev.Event {
		case "beforeSubmitPrompt":
			if turn.submittedAt == 0 || ev.ReceivedAtMs < turn.submittedAt {
				turn.submittedAt = ev.ReceivedAtMs
			}
		case "postToolUse", "postToolUseFailure", "afterAgentThought", "afterAgentResponse":
			if ev.ReceivedAtMs > turn.lastActivityAt {
				turn.lastActivityAt = ev.ReceivedAtMs
			}
		case "stop":
			key := ev.Status
			if key == "" {
				// An unreadable status still ended something, but it can't be read as success
				key = "unknown"
			}
			keepEarliest(turn.stops, key, ev.ReceivedAtMs)
		case "sessionEnd":
			key := ev.Reason
			if key == "" {
				key = "unknown"
			}
			keepEarliest(turn.ends, key, ev.ReceivedAtMs)
		}
	}
	return turns
}

func keepEarliest(m map[string]int64, key string, at int64) {
	if prev, ok := m[key]; !ok || at < prev {
		m[key] = at
	}
}

// pickCursorTurn chooses the turn the list should describe. A submission is a user message, so
// it competes with observed activity on the newest evidence; with no submission at all, two
// turns working at once is a conflict rather than a guess
func pickCursorTurn(turns map[string]*cursorTurn, now time.Time) (*cursorTurn, bool) {
	var submitted *cursorTurn
	tied := false
	anySubmitted := false
	for _, t := range turns {
		if t.submittedAt == 0 {
			continue
		}
		anySubmitted = true
		switch {
		case submitted == nil || t.submittedAt > submitted.submittedAt:
			submitted, tied = t, false
		case t.submittedAt == submitted.submittedAt:
			tied = true
		}
	}
	if tied {
		return nil, true
	}

	var active []*cursorTurn
	for _, t := range turns {
		if freshCursorActivity(t, now) {
			active = append(active, t)
		}
	}
	if !anySubmitted {
		switch len(active) {
		case 0:
		case 1:
			return active[0], false
		default:
			// Two generations working at once, and nothing saying which is current
			return nil, true
		}
	}

	// The turn with the newest evidence is the one being worked on. A generation that arrived
	// without a submission is how a follow-up turn looks, so it counts here
	best := submitted
	bestAt := int64(0)
	if best != nil {
		bestAt = best.latestEvidenceAt()
	}
	for _, t := range active {
		if t == best {
			continue
		}
		at := t.latestEvidenceAt()
		switch {
		case best == nil || at > bestAt:
			best, bestAt = t, at
		case at == bestAt:
			return nil, true
		}
	}
	if best != nil {
		return best, false
	}

	// Nothing is running: describe the turn with the newest evidence, which is the one the list
	// would otherwise be showing stale news from
	newestAt := int64(0)
	for _, t := range turns {
		at := t.newestEvidenceAt()
		switch {
		case at > newestAt:
			best, newestAt = t, at
		case at == newestAt && at != 0:
			return nil, true
		}
	}
	return best, false
}

// latestLiveEvidence is the newest submission or activity across every generation: a turn
// started after a session ended is a reopened conversation
func latestLiveEvidence(turns map[string]*cursorTurn) int64 {
	var newest int64
	for _, t := range turns {
		newest = max(newest, t.latestEvidenceAt())
	}
	return newest
}

// freshCursorActivity reports whether a turn looked active recently enough to still be running
func freshCursorActivity(t *cursorTurn, now time.Time) bool {
	if t.lastActivityAt == 0 {
		return false
	}
	return now.UnixMilli()-t.lastActivityAt <= cursorActivityTTL.Milliseconds()
}

// newestEvidenceAt is the latest thing known about a turn, ending included
func (t *cursorTurn) newestEvidenceAt() int64 {
	at, _ := t.terminalAt()
	return max(t.latestEvidenceAt(), at)
}
