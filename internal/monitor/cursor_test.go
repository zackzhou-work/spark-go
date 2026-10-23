package monitor

import "testing"

func TestCursorDeeplinkAddressesTheAgentById(t *testing.T) {
	got, ok := CursorDeeplink("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	want := "cursor://anysphere.cursor-deeplink/agent?id=0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
	if !ok || got != want {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestCursorDeeplinkRejectsIDsCursorWouldNotMatch(t *testing.T) {
	for _, id := range []string{
		"",
		// Claude's own ID shape, so a mix-up between sources can't produce a Cursor link
		"local_1f079f2a-8c74-449d-b254-1be4a5d9b0d8",
		// Cloud agent IDs carry a bc- prefix and live outside the desktop metadata
		"bc-0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		// Short non-UUID composer IDs exist in old headers but have no verified link
		"12345-67890-12345",
		"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0-extra",
		"zzzzzzzz-4b5a-6978-8796-a5b4c3d2e1f0",
		// Query-breaking characters must never reach the system opener
		"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0&x=1",
	} {
		if got, ok := CursorDeeplink(id); ok {
			t.Errorf("%q should be rejected, got %q", id, got)
		}
	}
}

func TestUnknownStateSaysItHasNoAnswer(t *testing.T) {
	if got := Unknown.String(); got != "Status unavailable" {
		t.Errorf("got %q", got)
	}
}

func TestUnknownSortsBelowUnreadAndAboveCompleted(t *testing.T) {
	order := []TaskState{Running, Waiting, Unread, Unknown, Completed}
	for i := 1; i < len(order); i++ {
		if order[i-1].priority() <= order[i].priority() {
			t.Errorf("%s should sort above %s", order[i-1], order[i])
		}
	}
}

func TestCursorIsItsOwnSource(t *testing.T) {
	if got := SourceCursor.String(); got != "Cursor" {
		t.Errorf("got %q", got)
	}
}

// A Cursor row is a click target only where the scanner proved the private Agent link works;
// anything else keeps the row static rather than opening a window that selects nothing
func TestCursorRowsAreClickTargetsOnlyWhenTheScannerAllowsIt(t *testing.T) {
	s := SessionItem{ID: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Source: SourceCursor}
	if s.Jumpable() {
		t.Error("a cursor row without CanJumpCursor should not be clickable")
	}
	s.CanJumpCursor = true
	if !s.Jumpable() {
		t.Error("a cursor row with CanJumpCursor should be clickable")
	}
}

// A Cursor build nobody has run the A/B test against keeps the private Agent link off: a URL
// that raises the window and selects nothing is indistinguishable from success here
func TestJumpStaysOffForUnverifiedBuilds(t *testing.T) {
	build := cursorBuild{Version: "3.24.13", Commit: "d5c0e77a0214208f36b56d42e8e787de88d02ea4"}
	if cursorJumpAllowed(build, nil, false) {
		t.Error("an unverified build should not offer the link")
	}
}

func TestJumpOpensForVerifiedBuildsAndForTheOptIn(t *testing.T) {
	verified := []cursorBuild{{Version: "3.24.12", Commit: "37b865941ccb31e2fc90c685157534efca87f960"}}
	// The shipped table names the build the validation record covers
	if !cursorJumpAllowed(verified[0], cursorVerifiedBuilds, false) {
		t.Error("the verified build should be in the shipped table")
	}
	if !cursorJumpAllowed(cursorBuild{Version: "3.24.12", Commit: "37b865941ccb31e2fc90c685157534efca87f960"}, verified, false) {
		t.Error("the verified commit should offer the link")
	}
	// A newer build is a different build: the table names commits, not a floor
	if cursorJumpAllowed(cursorBuild{Version: "3.25.0", Commit: "37b865941ccb31e2fc90c685157534efca87f960"}, verified, false) {
		t.Error("only the tested version should offer the link")
	}
	if cursorJumpAllowed(cursorBuild{Version: "3.24.12", Commit: "37b865941ccb31e2fc90c685157534efca87f960"}, nil, true) == false {
		t.Error("the documented opt-in should offer the link on an unverified build")
	}
}
