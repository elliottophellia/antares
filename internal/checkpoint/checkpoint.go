// Package checkpoint keeps a copy of every file the agent is about to change,
// so a bad edit can be undone.
//
// An agent that writes files and cannot undo is a frightening thing to leave
// running. Version control covers the case where the work is in a repository
// and was committed; this covers the other cases, which is most of them while
// work is actually happening.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one saved file version.
type Entry struct {
	// Path is the file as it was addressed.
	Path string `json:"path"`
	// Blob is the stored copy, empty when the file did not exist yet.
	Blob string `json:"blob,omitempty"`
	// Existed distinguishes "was empty" from "was not there", which restore
	// has to get right: the second means delete.
	Existed bool      `json:"existed"`
	Mode    uint32    `json:"mode"`
	SavedAt time.Time `json:"saved_at"`
	// Tool names what was about to touch it.
	Tool string `json:"tool,omitempty"`
}

// Checkpoint is everything one session changed, in order.
type Checkpoint struct {
	SessionID string  `json:"session_id"`
	Entries   []Entry `json:"entries"`
}

// Store writes checkpoints under a root directory.
type Store struct {
	root string

	mu sync.Mutex
	// seen stops a file being copied twice in one session: the first copy is
	// the one worth keeping, since it is the state before any of this ran.
	seen map[string]map[string]bool
}

// NewStore builds a store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{root: dir, seen: map[string]map[string]bool{}}
}

func (s *Store) sessionDir(sessionID string) string {
	return filepath.Join(s.root, safeName(sessionID))
}

// Save copies a file before it is changed. A path already saved in this
// session is left alone, so the checkpoint holds the state before the agent
// started rather than before its most recent edit.
func (s *Store) Save(sessionID, path, tool string) error {
	if s == nil || s.root == "" || sessionID == "" || path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	s.mu.Lock()
	if s.seen[sessionID] == nil {
		s.seen[sessionID] = map[string]bool{}
	}
	if s.seen[sessionID][abs] {
		s.mu.Unlock()
		return nil
	}
	s.seen[sessionID][abs] = true
	s.mu.Unlock()

	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return err
	}

	entry := Entry{Path: abs, SavedAt: time.Now(), Tool: tool}
	if info, err := os.Stat(abs); err == nil && info.Mode().IsRegular() {
		// A file too large to copy cheaply is recorded as unrecoverable rather
		// than silently skipped, so restore can say so.
		const maxCopy = 32 << 20
		if info.Size() <= maxCopy {
			content, err := os.ReadFile(abs)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			name := hex.EncodeToString(sum[:])
			blob := filepath.Join(dir, "blobs", name)
			if _, err := os.Stat(blob); os.IsNotExist(err) {
				if err := os.WriteFile(blob, content, 0o600); err != nil {
					return err
				}
			}
			entry.Blob = name
		}
		entry.Existed = true
		entry.Mode = uint32(info.Mode().Perm())
	}

	return s.append(dir, entry)
}

// append adds an entry to the session's log.
func (s *Store) append(dir string, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(filepath.Join(dir, "entries.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	return err
}

// Load reads a session's checkpoint.
func (s *Store) Load(sessionID string) (*Checkpoint, error) {
	dir := s.sessionDir(sessionID)
	raw, err := os.ReadFile(filepath.Join(dir, "entries.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return &Checkpoint{SessionID: sessionID}, nil
		}
		return nil, err
	}
	cp := &Checkpoint{SessionID: sessionID}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil {
			cp.Entries = append(cp.Entries, e)
		}
	}
	return cp, nil
}

// RestoreResult reports what a rollback did.
type RestoreResult struct {
	Restored []string
	Deleted  []string
	Failed   map[string]string
}

// Restore puts a session's files back the way they were. Files created during
// the session are deleted, because "back the way it was" means not existing.
//
// Passing paths restores only those; passing none restores everything.
func (s *Store) Restore(sessionID string, paths []string) (*RestoreResult, error) {
	cp, err := s.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if len(cp.Entries) == 0 {
		return nil, errors.New("nothing was changed in this session")
	}

	want := map[string]bool{}
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			want[abs] = true
		} else {
			want[p] = true
		}
	}

	out := &RestoreResult{Failed: map[string]string{}}
	dir := s.sessionDir(sessionID)

	// Earliest entry per path is the original state.
	first := map[string]Entry{}
	for _, e := range cp.Entries {
		if _, ok := first[e.Path]; !ok {
			first[e.Path] = e
		}
	}

	names := make([]string, 0, len(first))
	for p := range first {
		names = append(names, p)
	}
	sort.Strings(names)

	for _, path := range names {
		if len(want) > 0 && !want[path] {
			continue
		}
		e := first[path]

		if !e.Existed {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				out.Failed[path] = err.Error()
				continue
			}
			out.Deleted = append(out.Deleted, path)
			continue
		}
		if e.Blob == "" {
			out.Failed[path] = "the original was too large to keep a copy of"
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, "blobs", e.Blob))
		if err != nil {
			out.Failed[path] = err.Error()
			continue
		}
		mode := os.FileMode(e.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			out.Failed[path] = err.Error()
			continue
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			out.Failed[path] = err.Error()
			continue
		}
		out.Restored = append(out.Restored, path)
	}

	if len(out.Restored) == 0 && len(out.Deleted) == 0 && len(out.Failed) > 0 {
		return out, fmt.Errorf("nothing could be restored")
	}
	return out, nil
}

// Clear forgets a session's checkpoint.
func (s *Store) Clear(sessionID string) error {
	s.mu.Lock()
	delete(s.seen, sessionID)
	s.mu.Unlock()
	return os.RemoveAll(s.sessionDir(sessionID))
}

// Prune removes checkpoints older than the given age. Left alone these grow
// without bound, since every session that touched a file leaves one.
func (s *Store) Prune(olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.RemoveAll(filepath.Join(s.root, e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

// safeName keeps a session id from escaping the checkpoint root.
func safeName(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
	if out == "" {
		return "unknown"
	}
	return out
}
