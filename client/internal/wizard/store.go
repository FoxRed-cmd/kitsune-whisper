package wizard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileStore keeps wizard progress in one JSON file next to the effective
// kitsune.yaml. Saves are atomic: a sibling temporary file with 0600 is synced
// and renamed into place, so a crash cannot leave a half-written state.
type FileStore struct {
	// Path is the wizard.json path.
	Path string
	// OnLog receives corrupt-file and backup messages; nil is silent.
	OnLog func(string)
}

// NewFileStore returns a FileStore for path.
func NewFileStore(path string) *FileStore { return &FileStore{Path: path} }

// Load reads the state file. A missing file is not an error. A corrupt file is
// renamed to <path>.bak and reported so Open can start a fresh wizard instead
// of crashing the Client.
func (s *FileStore) Load() (State, bool, error) {
	if s.Path == "" {
		return State{}, false, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, false, nil
		}
		return State{}, true, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		backup := s.Path + ".bak"
		if renameErr := os.Rename(s.Path, backup); renameErr != nil {
			s.logf("could not back up the corrupt setup progress: %v", renameErr)
		} else {
			s.logf("the setup progress was corrupt; moved it to %s", backup)
		}
		return State{}, true, fmt.Errorf("wizard: decode %s: %w", s.Path, err)
	}
	return state, true, nil
}

// Save atomically writes state with 0600 permissions.
func (s *FileStore) Save(state State) error {
	if s.Path == "" {
		return fmt.Errorf("wizard: no setup progress path")
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("wizard: create the state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("wizard: encode the setup progress: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(dir, ".wizard-*.tmp")
	if err != nil {
		return fmt.Errorf("wizard: create a temporary state file: %w", err)
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("wizard: set state permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("wizard: write the setup progress: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("wizard: sync the setup progress: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("wizard: close the setup progress: %w", err)
	}
	if err := os.Rename(tempName, s.Path); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("wizard: replace the setup progress: %w", err)
	}
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

func (s *FileStore) logf(format string, args ...any) {
	if s.OnLog != nil {
		s.OnLog(fmt.Sprintf(format, args...))
	}
}
