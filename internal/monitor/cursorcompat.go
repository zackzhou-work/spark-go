package monitor

import "os"

// cursorBuild is one Cursor build whose private Agent link spark has verified by hand
type cursorBuild struct {
	Version string
	Commit  string
}

// cursorVerifiedBuilds is what the validation record has proved, one entry per build that was
// actually clicked through. A build that isn't here gets no link: raising the window is not proof
// that the session it was asked for got selected
//
// 3.24.12 was verified by hand against two conversations in one project and a third in another:
// each link selected the conversation it named, and an ID that doesn't exist raised Cursor's own
// error without starting a chat. See docs/research/cursor-integration-validation.md
var cursorVerifiedBuilds = []cursorBuild{
	{Version: "3.24.12", Commit: "37b865941ccb31e2fc90c685157534efca87f960"},
}

// cursorJumpOptIn is the documented escape hatch for running that check: it turns the link on
// for every build so the owner can click A and B and see which one Cursor selects
const cursorJumpOptIn = "SPARK_CURSOR_JUMP"

// CursorJumpAllowed reports whether this build's private Agent link may be offered
func CursorJumpAllowed(version, commit string) bool {
	return cursorJumpAllowed(cursorBuild{Version: version, Commit: commit}, cursorVerifiedBuilds, os.Getenv(cursorJumpOptIn) != "")
}

func cursorJumpAllowed(build cursorBuild, verified []cursorBuild, optIn bool) bool {
	if optIn {
		return true
	}
	for _, v := range verified {
		if v == build {
			return true
		}
	}
	return false
}
