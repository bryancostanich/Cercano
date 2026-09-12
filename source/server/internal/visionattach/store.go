// Package visionattach stores conversation images in private OS temporary files
// for inspection by stable ID. Only metadata stays in memory; Lookup reads bytes
// on demand. There are no cumulative conversation image-count or byte caps.
//
// IDs are process-local, not persisted. Missing temporary files and IDs after a
// restart are expected lookup misses; callers should ask users to reattach.
// Owners must Close the store when finished. Crashes may leave temporary files
// for OS cleanup; this is not durable conversation attachment storage.
package visionattach

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
)

// Attachment is image metadata plus caller-owned bytes populated only by lookup.
type Attachment struct {
	ID        string
	Ordinal   int
	MediaType string
	Data      []byte
	Hash      string
}

type entry struct {
	metadata Attachment // Data is always nil; never retain payload bytes.
	path     string
}

// Store is safe for concurrent use. Disk operations are serialized with cleanup.
type Store struct {
	mu     sync.Mutex
	convs  map[string]*convTable
	dir    string // lazily created, private directory owned exclusively by this store
	closed bool
}

type convTable struct {
	byID    map[string]*entry
	byHash  map[string]*entry
	nextOrd int
}

// NewStore defers temporary directory creation until the first image is added.
// Creation failures are reported by Add, just like image write failures.
func NewStore() *Store { return &Store{convs: make(map[string]*convTable)} }

type AddResult struct {
	Attachment   *Attachment // metadata only
	Deduped      bool
	Rejected     bool
	RejectReason string
}

func metadata(e *entry) *Attachment { a := e.metadata; return &a }

// write creates a private file and publishes it only after a successful close.
// The caller holds mu. No user-supplied identifier is used as a filesystem path.
func (s *Store) write(data []byte) (string, error) {
	// A temporary-file cleaner may remove the whole directory. Allocate a new
	// private directory rather than recreating a predictable old pathname.
	if s.dir != "" {
		if _, err := os.Stat(s.dir); os.IsNotExist(err) {
			s.dir = ""
		}
	}
	if s.dir == "" {
		dir, err := os.MkdirTemp("", "cercano-images-")
		if err != nil {
			return "", err
		}
		s.dir = dir
	}
	f, err := os.CreateTemp(s.dir, "image-")
	if err != nil {
		return "", err
	}
	path := f.Name()
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// Add writes an image to disk and returns metadata. Identical bytes within a
// conversation reuse their ID. Reattaching restores an externally removed file.
func (s *Store) Add(convID, mediaType string, data []byte) AddResult {
	if convID == "" {
		return AddResult{Rejected: true, RejectReason: "no conversation id"}
	}
	if len(data) == 0 {
		return AddResult{Rejected: true, RejectReason: "empty image"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return AddResult{Rejected: true, RejectReason: "attachment store is closed"}
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	t := s.convs[convID]
	if t != nil {
		if existing := t.byHash[hash]; existing != nil {
			if st, err := os.Stat(existing.path); err == nil && st.Mode().IsRegular() && st.Size() == int64(len(data)) {
				return AddResult{Attachment: metadata(existing), Deduped: true}
			}
			path, err := s.write(data)
			if err != nil {
				return AddResult{Rejected: true, RejectReason: "temporary image storage unavailable"}
			}
			_ = os.Remove(existing.path)
			existing.path = path
			return AddResult{Attachment: metadata(existing), Deduped: true}
		}
	}
	path, err := s.write(data)
	if err != nil {
		return AddResult{Rejected: true, RejectReason: "temporary image storage unavailable"}
	}
	if t == nil {
		t = &convTable{byID: map[string]*entry{}, byHash: map[string]*entry{}, nextOrd: 1}
		s.convs[convID] = t
	}
	e := &entry{metadata: Attachment{ID: fmt.Sprintf("img_%s_%d", hash[:6], t.nextOrd), Ordinal: t.nextOrd, MediaType: mediaType, Hash: hash}, path: path}
	t.byID[e.metadata.ID] = e
	t.byHash[hash] = e
	t.nextOrd++
	return AddResult{Attachment: metadata(e)}
}

func load(e *entry) (*Attachment, bool) {
	if e == nil {
		return nil, false
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		return nil, false
	}
	a := metadata(e)
	a.Data = data
	return a, true
}

// Lookup loads caller-owned bytes. Missing or unreadable files are lookup misses.
func (s *Store) Lookup(convID, id string) (*Attachment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.convs[convID]; t != nil {
		return load(t.byID[id])
	}
	return nil, false
}

// LookupAny refuses ambiguous conversation-scoped IDs rather than guessing.
func (s *Store) LookupAny(id string) (att *Attachment, convID string, ok bool, ambiguous bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *entry
	for cid, t := range s.convs {
		if e := t.byID[id]; e != nil {
			if found != nil {
				return nil, "", false, true
			}
			found, convID = e, cid
		}
	}
	att, ok = load(found)
	if !ok {
		return nil, "", false, false
	}
	return att, convID, true, false
}

// Clear removes a conversation's files and metadata. Failed deletions remain
// inside the owned directory and are retried by Close.
func (s *Store) Clear(convID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.convs[convID]; t != nil {
		for _, e := range t.byID {
			_ = os.Remove(e.path)
		}
	}
	delete(s.convs, convID)
}

// Close releases metadata and removes all owned files. It is idempotent; failed
// cleanup may be retried. A closed store rejects new attachments.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	clear(s.convs)
	if s.dir == "" {
		return nil
	}
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	s.dir = ""
	return nil
}

// Count reports registered images, including ones externally removed from disk.
func (s *Store) Count(convID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.convs[convID]; t != nil {
		return len(t.byID)
	}
	return 0
}
