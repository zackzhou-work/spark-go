package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// charactersVersion is the file layout this build reads and writes
const charactersVersion = 1

// characterChoice is one project's saved character as the file names it: stable ids, so a
// shape or colour added in the middle of the catalogue doesn't move anyone's character
type characterChoice struct {
	Shape string `json:"shape"`
	Color string `json:"color"`
}

// characterSettings is the characters saved per project. Projects are grouped by name, so two
// checkouts of one name share a character
type characterSettings struct {
	Version  int                        `json:"version"`
	Projects map[string]characterChoice `json:"projects"`
}

func charactersPath(home string) string {
	return filepath.Join(home, ".config", "spark", "characters.json")
}

// character resolves a project's saved choice; ok is false when the project has none or the
// choice names a shape or colour this build doesn't have
func (s characterSettings) character(project string) (character, bool) {
	choice, ok := s.Projects[project]
	if !ok {
		return character{}, false
	}
	shape, colour := bodyIndex(choice.Shape), colourIndex(choice.Color)
	if shape < 0 || colour < 0 {
		return character{}, false
	}
	return character{hue: colour, shape: shape}, true
}

// with returns a copy of s saving ch for project, so what the app holds stays what is on disk
// until the write succeeds
func (s characterSettings) with(project string, ch character) characterSettings {
	projects := make(map[string]characterChoice, len(s.Projects)+1)
	for name, choice := range s.Projects {
		projects[name] = choice
	}
	projects[project] = characterChoice{Shape: bodyID(ch.shape), Color: colourID(ch.hue)}
	return characterSettings{Version: charactersVersion, Projects: projects}
}

// readCharacterSettings loads the saved characters. A missing file is no choices; anything
// else that stops it being read is an error with no choices, so a file this build can't
// understand is never written over
func readCharacterSettings(path string) (characterSettings, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return characterSettings{}, nil
	case err != nil:
		return characterSettings{}, err
	}
	return parseCharacterSettings(data)
}

// parseCharacterSettings reads saved characters, dropping the projects whose shape or colour
// this build doesn't know: those fall back to the automatic character, the rest still apply
func parseCharacterSettings(data []byte) (characterSettings, error) {
	var file characterSettings
	if err := json.Unmarshal(data, &file); err != nil {
		return characterSettings{}, fmt.Errorf("not a characters file: %w", err)
	}
	if file.Version != charactersVersion {
		return characterSettings{}, fmt.Errorf("characters file version %d, want %d", file.Version, charactersVersion)
	}
	settings := characterSettings{Version: file.Version, Projects: make(map[string]characterChoice, len(file.Projects))}
	for project, choice := range file.Projects {
		if bodyIndex(choice.Shape) < 0 || colourIndex(choice.Color) < 0 {
			continue
		}
		settings.Projects[project] = choice
	}
	return settings, nil
}

// saveCharacterSettings writes the choices beside the file and renames them over it, so a
// crash leaves either the old choices or the new ones, never half of either
func saveCharacterSettings(path string, settings characterSettings) error {
	if path == "" {
		return errors.New("no characters file to write")
	}
	settings.Version = charactersVersion
	if settings.Projects == nil {
		settings.Projects = map[string]characterChoice{}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".characters-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
