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
	// Marker groups entries by the conversation turn that produced them — the id
	// of the user message that opened the turn. It lets a rollback target "the
	// state as of message X" rather than only the start of the whole session.
	// Empty on entries written before markers existed (treated as oldest).
	Marker string `json:"marker,omitempty"`
	// ResultHash is the sha256 of what the agent WROTE to this path in this turn,
	// recorded just after the write. It is how we tell later whether the file was
	// edited outside the session: if the current file matches a ResultHash, the
	// session produced that state; if it matches none, someone else changed it.
	ResultHash string `json:"result_hash,omitempty"`
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

// Save copies a file before it is changed. It records at most one version per
// file per turn (marker): the first save within a turn is the file's state as
// that turn began. Across turns it keeps one snapshot each, so a rollback can
// return a file to how it was as of any given message.
//
// marker is the id of the user message that opened the current turn; pass "" if
// unknown (those entries are treated as the oldest state).
func (s *Store) Save(sessionID, path, tool, marker string) error {
	if s == nil || s.root == "" || sessionID == "" || path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	// Dedup per (marker, path): the first write of a file within a turn captures
	// its pre-turn state; later writes in the SAME turn are already covered.
	key := marker + "\x00" + abs
	s.mu.Lock()
	if s.seen[sessionID] == nil {
		s.seen[sessionID] = map[string]bool{}
	}
	if s.seen[sessionID][key] {
		s.mu.Unlock()
		return nil
	}
	s.seen[sessionID][key] = true
	s.mu.Unlock()

	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return err
	}

	entry := Entry{Path: abs, SavedAt: time.Now(), Tool: tool, Marker: marker}
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

// RecordResult notes the hash of what the agent just wrote to a path, so a
// later rollback can tell whether the file was since edited outside the session.
// It appends a result-only entry (no blob), identified by ResultHash being set.
func (s *Store) RecordResult(sessionID, path, marker, resultHash string) error {
	if s == nil || s.root == "" || sessionID == "" || path == "" || resultHash == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return s.append(dir, Entry{Path: abs, Marker: marker, ResultHash: resultHash, SavedAt: time.Now()})
}

// isResult reports whether an entry is a result-hash record rather than a
// pre-write snapshot. Snapshot logic (baseline, restore) must skip these.
func (e Entry) isResult() bool { return e.ResultHash != "" }

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

	// Earliest snapshot per path is the original state (result-hash records are
	// not snapshots — skip them).
	first := map[string]Entry{}
	for _, e := range cp.Entries {
		if e.isResult() {
			continue
		}
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

// ChangedSince describes one file touched at or after a marker, for the
// edit-message preview.
type ChangedSince struct {
	Path string `json:"path"`
	// ExternallyChanged is true when the file on disk no longer matches the last
	// version the agent recorded — someone edited it outside this session, so a
	// revert would clobber that. RestoreSince skips these when asked to.
	ExternallyChanged bool `json:"externally_changed"`
	// WillDelete is true when reverting means removing the file (it did not exist
	// before the marker).
	WillDelete bool `json:"will_delete"`
}

// entriesByPath groups a checkpoint's entries by path, preserving order.
func entriesByPath(cp *Checkpoint) map[string][]Entry {
	m := map[string][]Entry{}
	for _, e := range cp.Entries {
		m[e.Path] = append(m[e.Path], e)
	}
	return m
}

// baselineBefore returns the snapshot representing a path's state as the marker
// turn BEGAN — i.e. the content to restore when reverting "to just before this
// message". Each turn's first snapshot is the pre-write copy taken before that
// turn touched the file, which equals the file's state at the end of the prior
// turn. So the baseline is the marker turn's OWN first snapshot. Returns
// (entry, true) when the marker turn snapshotted this path; (_, false) when it
// did not (the file was only ever touched in a later turn — nothing to restore
// to, so the caller treats a missing baseline accordingly).
func baselineBefore(entries []Entry, marker string) (Entry, bool) {
	for _, e := range entries {
		if e.isResult() {
			continue
		}
		if e.Marker == marker {
			return e, true
		}
	}
	return Entry{}, false
}

// touchedSince reports whether a path has any snapshot at/after the marker —
// i.e. it was changed during or after that turn.
func touchedSince(entries []Entry, marker string) bool {
	seenMarker := false
	for _, e := range entries {
		if e.isResult() {
			continue
		}
		if e.Marker == marker {
			seenMarker = true
		}
		if seenMarker {
			return true
		}
	}
	return false
}

// PreviewSince lists the files changed at or after `marker`, flagging which were
// edited outside the session (so the caller can warn or skip them).
func (s *Store) PreviewSince(sessionID, marker string) ([]ChangedSince, error) {
	cp, err := s.Load(sessionID)
	if err != nil {
		return nil, err
	}
	byPath := entriesByPath(cp)
	dir := s.sessionDir(sessionID)
	var out []ChangedSince
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		entries := byPath[path]
		if !touchedSince(entries, marker) {
			continue
		}
		base, haveBaseline := baselineBefore(entries, marker)
		out = append(out, ChangedSince{
			Path:              path,
			WillDelete:        !haveBaseline || !base.Existed,
			ExternallyChanged: fileDiffersFromLastRecorded(dir, path, entries),
		})
	}
	return out, nil
}

// RestoreSince reverts files touched at/after `marker` to their state just
// before it. When skipExternallyChanged is set, a file whose current contents no
// longer match the agent's last recorded write is left untouched.
func (s *Store) RestoreSince(sessionID, marker string, skipExternallyChanged bool) (*RestoreResult, error) {
	cp, err := s.Load(sessionID)
	if err != nil {
		return nil, err
	}
	if len(cp.Entries) == 0 {
		return nil, errors.New("nothing was changed in this session")
	}
	byPath := entriesByPath(cp)
	dir := s.sessionDir(sessionID)
	out := &RestoreResult{Failed: map[string]string{}}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		entries := byPath[path]
		if !touchedSince(entries, marker) {
			continue
		}
		if skipExternallyChanged && fileDiffersFromLastRecorded(dir, path, entries) {
			continue
		}
		base, existedBefore := baselineBefore(entries, marker)
		if !existedBefore || !base.Existed {
			// The file did not exist before this marker's turn — remove it.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				out.Failed[path] = err.Error()
				continue
			}
			out.Deleted = append(out.Deleted, path)
			continue
		}
		if base.Blob == "" {
			out.Failed[path] = "the original was too large to keep a copy of"
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, "blobs", base.Blob))
		if err != nil {
			out.Failed[path] = err.Error()
			continue
		}
		mode := os.FileMode(base.Mode)
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
	return out, nil
}

// fileDiffersFromLastRecorded reports whether the file on disk looks edited
// OUTSIDE the session since the agent last touched it.
//
// The checkpoint only stores PRE-write snapshots, never the agent's output. But
// a turn's pre-write snapshot equals the previous turn's post-write result, so
// the set of recorded blobs contains every intermediate the agent produced —
// except the very last write of the session. We therefore call it externally
// changed only when the current content matches NONE of the recorded blobs: if
// it matches any snapshot, the file is in a state the session itself produced
// (conservative — we would rather revert than wrongly skip). A newly-created
// file (no prior blob) with content not matching later snapshots is treated as
// the agent's own and revertible.
func fileDiffersFromLastRecorded(dir, path string, entries []Entry) bool {
	cur, err := os.ReadFile(path)
	if err != nil {
		return false // gone or unreadable — nothing to protect
	}
	sum := sha256.Sum256(cur)
	curHash := hex.EncodeToString(sum[:])

	// The result hashes are exactly what the agent wrote across this session's
	// turns. If the current file matches any of them, the session produced this
	// state (revertible). If it matches none but we DID record results, someone
	// edited it outside the session → protect it.
	sawResult := false
	for _, e := range entries {
		if e.ResultHash == "" {
			continue
		}
		sawResult = true
		if e.ResultHash == curHash {
			return false
		}
	}
	if sawResult {
		return true
	}
	// No result hashes recorded (older checkpoints): fall back to the pre-write
	// blobs. Unknown content with a known prior blob ⇒ treat as external.
	sawBlob := false
	for _, e := range entries {
		if e.Blob == "" {
			continue
		}
		sawBlob = true
		if e.Blob == curHash {
			return false
		}
	}
	return sawBlob
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
