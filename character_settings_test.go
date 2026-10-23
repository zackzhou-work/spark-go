package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func writeCharactersFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "characters.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A missing file means nobody has chosen a character yet, which is not a failure
func TestMissingCharactersFileIsNoChoices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "characters.json")
	settings, err := readCharacterSettings(path)
	if err != nil {
		t.Fatalf("a missing file should be no choices, got %v", err)
	}
	if len(settings.Projects) != 0 {
		t.Fatalf("a missing file should be no choices, got %v", settings.Projects)
	}
}

// One bad entry falls back to the automatic character on its own; it doesn't take the file
// with it
func TestCharactersFileDropsChoicesThatNameAnUnknownShapeOrColour(t *testing.T) {
	path := writeCharactersFile(t, `{
		"version": 1,
		"projects": {
			"spark-go":  {"shape": "cloud", "color": "pink"},
			"other":     {"shape": "hexagon", "color": "pink"},
			"another":   {"shape": "cloud", "color": "chartreuse"},
			"empty":     {},
			"caps":      {"shape": "CLOUD", "color": "Pink"}
		}
	}`)
	settings, err := readCharacterSettings(path)
	if err != nil {
		t.Fatalf("unknown entries should not fail the file: %v", err)
	}
	if len(settings.Projects) != 1 {
		t.Fatalf("only the valid choice should survive, got %v", settings.Projects)
	}
	want := character{hue: colourIndex("pink"), shape: bodyIndex("cloud")}
	if got, ok := settings.character("spark-go"); !ok || got != want {
		t.Fatalf("spark-go should keep its choice %v, got %v %v", want, got, ok)
	}
	if _, ok := settings.character("other"); ok {
		t.Error("an unknown shape should leave the project without a choice")
	}
}

func TestCharactersFileRejectsABrokenFile(t *testing.T) {
	path := writeCharactersFile(t, `{"version": 1, "projects": {"spark-go": `)
	if _, err := readCharacterSettings(path); err == nil {
		t.Fatal("a file that isn't JSON should be reported, not read as no choices")
	}
}

func TestCharactersFileRejectsAnUnsupportedVersion(t *testing.T) {
	for _, body := range []string{
		`{"version": 2, "projects": {}}`,
		`{"projects": {"spark-go": {"shape": "cloud", "color": "pink"}}}`,
		`[]`,
	} {
		path := writeCharactersFile(t, body)
		if _, err := readCharacterSettings(path); err == nil {
			t.Errorf("%s should be reported as unreadable", body)
		}
	}
}

func TestSavingChoicesWritesAFileThatReadsBackTheSame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spark", "characters.json")
	settings := characterSettings{}.with("spark-go", character{hue: colourIndex("pink"), shape: bodyIndex("cloud")})
	if err := saveCharacterSettings(path, settings); err != nil {
		t.Fatalf("saving choices failed: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"version": 1.0,
		"projects": map[string]any{
			"spark-go": map[string]any{"shape": "cloud", "color": "pink"},
		},
	}
	if !mapsEqual(raw, want) {
		t.Fatalf("the file should name stable ids, got %s", body)
	}

	back, err := readCharacterSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	want2, _ := settings.character("spark-go")
	if got, ok := back.character("spark-go"); !ok || got != want2 {
		t.Fatalf("the saved choice should read back as itself, got %v %v", got, ok)
	}
}

// Saving replaces the file in one step: the old choices are never half written, and no
// temporary file is left behind
func TestSavingChoicesReplacesTheFileInOneStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "characters.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"projects":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := characterSettings{}.with("spark-go", character{hue: colourIndex("black"), shape: bodyIndex("star")})
	if err := saveCharacterSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"characters.json"}) {
		t.Fatalf("saving should leave the choices and nothing else, got %v", names)
	}
	if got, _ := readCharacterSettings(path); len(got.Projects) != 1 {
		t.Fatalf("the new choices should be in the file, got %v", got.Projects)
	}
}

func TestSavingChoicesReportsAFileItCannotReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "characters.json")
	if err := os.MkdirAll(filepath.Join(path, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := saveCharacterSettings(path, characterSettings{}.with("spark-go", character{hue: colourIndex("gray"), shape: bodyIndex("heart")}))
	if err == nil {
		t.Fatal("saving over something that isn't the file should fail")
	}
	if _, statErr := os.Stat(filepath.Join(path, "keep")); statErr != nil {
		t.Fatalf("a failed save should leave what was there: %v", statErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("a failed save should leave no temporary file behind, got %v", entries)
	}
}

func TestSavingChoicesNeedsAFileToWrite(t *testing.T) {
	if err := saveCharacterSettings("", characterSettings{}); err == nil {
		t.Fatal("saving with no path should fail rather than write somewhere unexpected")
	}
}

// The file stores ids, not array positions, so inserting a shape or colour doesn't move
// anyone's character
func TestShapeAndColourIDsAreStable(t *testing.T) {
	wantShapes := []string{"circle", "droplet", "square", "rice-ball", "cloud", "capsule", "star", "heart"}
	if len(bodies) != len(wantShapes) {
		t.Fatalf("%d bodies, want %d", len(bodies), len(wantShapes))
	}
	for i, id := range wantShapes {
		if got := bodyID(i); got != id {
			t.Errorf("body %d is %q, want %q", i, got, id)
		}
		if got := bodyIndex(id); got != i {
			t.Errorf("shape %q is %d, want %d", id, got, i)
		}
	}
	if bodyIndex("hexagon") != -1 || bodyID(-1) != "" {
		t.Error("unknown shape ids should not resolve")
	}

	wantColours := []struct{ id, name, hex string }{
		{"black", "Black", "#000000"},
		{"brown", "Brown", "#A16C39"},
		{"red", "Red", "#FB2C3F"},
		{"orange", "Orange", "#FE6A10"},
		{"yellow", "Yellow", "#FFBF01"},
		{"green", "Green", "#13C16F"},
		{"teal", "Teal", "#15B8A3"},
		{"blue", "Blue", "#429CFB"},
		{"purple", "Purple", "#905AFA"},
		{"pink", "Pink", "#F93498"},
		{"gray", "Gray", "#959595"},
	}
	if len(colours) != len(wantColours) {
		t.Fatalf("%d colours, want %d", len(colours), len(wantColours))
	}
	for i, want := range wantColours {
		c := colours[i]
		if c.id != want.id || c.name != want.name || c.hex != want.hex {
			t.Errorf("colour %d is %+v, want %+v", i, c, want)
		}
		if got := colourIndex(want.id); got != i {
			t.Errorf("colour %q is %d, want %d", want.id, got, i)
		}
		if c.body != ui.Hex(want.hex) {
			t.Errorf("%s draws %v, want %s", c.name, c.body, want.hex)
		}
	}
	if colourIndex("chartreuse") != -1 {
		t.Error("an unknown colour id should not resolve")
	}
}

// Body-drawn lines go white only where black would be lost in the body; eye whites and their
// pupils keep their own colours
func TestFaceLinesPickTheContrastingInk(t *testing.T) {
	for _, c := range colours {
		want := float32(0)
		if c.id == "black" {
			want = 1
		}
		if got := faceInk(ui.Hex(c.hex)); got != want {
			t.Errorf("%s lines are %v, want %v", c.name, got, want)
		}
	}
	if faceLine(0) != eyeInk {
		t.Error("ink lines should be the ink the rest of the face uses")
	}
	if faceLine(1) != eyeWhite {
		t.Error("white lines should be the eye white")
	}
}

// mapsEqual compares maps of decoded json values, including their nested maps
func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		am, aok := av.(map[string]any)
		bm, bok := bv.(map[string]any)
		switch {
		case aok && bok:
			if !mapsEqual(am, bm) {
				return false
			}
		case aok != bok || av != bv:
			return false
		}
	}
	return true
}
