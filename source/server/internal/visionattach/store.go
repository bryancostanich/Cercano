// Package visionattach holds a per-conversation, in-memory store of image
// attachments so a text reasoning model (which never sees the raw image) can ask
// a vision model about an image by a stable ID via the inspect_image tool.
//
// Scope and lifetime (V1): the store lives only while the process is running.
// Nothing here is persisted to SQLite or disk. On restart/resume the store is
// empty, so image IDs from a resumed conversation resolve to "not found" — the
// tool surfaces that as a clear reattach message rather than crashing. Callers
// must treat Lookup misses as an expected, recoverable condition.
package visionattach

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// Defaults bound per-conversation memory. A single conversation cannot pin more
// than MaxImagesPerConversation images or MaxBytesPerConversation total bytes;
// attachments beyond the cap are rejected by Add (the caller emits an omitted
// placeholder instead of storing them).
const (
	DefaultMaxImagesPerConversation = 20
	DefaultMaxBytesPerConversation  = 256 << 20 // 256 MiB
)

// Attachment is one stored image plus the metadata tools need.
type Attachment struct {
	ID        string // conversation-unique, e.g. "img_7f3a9c_1"
	Ordinal   int    // 1-based order within the conversation
	MediaType string // e.g. "image/png"
	Data      []byte // raw (decoded) image bytes
	Hash      string // hex sha256 of Data, used in the ID and for dedup
}

// Store is a set of per-conversation attachment tables, safe for concurrent use.
type Store struct {
	maxImages int
	maxBytes  int64

	mu    sync.Mutex
	convs map[string]*convTable
}

type convTable struct {
	byID       map[string]*Attachment
	byHash     map[string]*Attachment // dedup: identical bytes reuse one ID
	order      []string               // insertion order of IDs
	totalBytes int64
	nextOrd    int
}

// NewStore builds a store with the default caps. Pass zero to WithCaps to keep a
// default.
func NewStore() *Store {
	return &Store{
		maxImages: DefaultMaxImagesPerConversation,
		maxBytes:  DefaultMaxBytesPerConversation,
		convs:     make(map[string]*convTable),
	}
}

// WithCaps overrides the per-conversation caps (any non-positive value keeps the
// current setting). Intended for tests and tuning.
func (s *Store) WithCaps(maxImages int, maxBytes int64) *Store {
	if maxImages > 0 {
		s.maxImages = maxImages
	}
	if maxBytes > 0 {
		s.maxBytes = maxBytes
	}
	return s
}

// AddResult reports what happened to one Add call.
type AddResult struct {
	Attachment *Attachment
	// Deduped is true when identical bytes were already stored for this
	// conversation and the existing attachment/ID was returned.
	Deduped bool
	// Rejected is true when a cap (image count or total bytes) would be
	// exceeded; Attachment is nil and the caller should omit the image with a
	// clear placeholder rather than store it.
	Rejected bool
	// RejectReason explains a rejection for the user-facing placeholder.
	RejectReason string
}

// Add stores one image for a conversation and returns its attachment. Identical
// bytes within the same conversation dedup to the existing ID. Exceeding a cap
// rejects the image (no partial state) so the caller can emit an omitted
// placeholder. A blank convID or empty data is rejected.
func (s *Store) Add(convID string, mediaType string, data []byte) AddResult {
	if convID == "" {
		return AddResult{Rejected: true, RejectReason: "no conversation id"}
	}
	if len(data) == 0 {
		return AddResult{Rejected: true, RejectReason: "empty image"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	t := s.convs[convID]
	if t == nil {
		t = &convTable{byID: map[string]*Attachment{}, byHash: map[string]*Attachment{}, nextOrd: 1}
		s.convs[convID] = t
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if existing, ok := t.byHash[hash]; ok {
		return AddResult{Attachment: existing, Deduped: true}
	}

	if len(t.order) >= s.maxImages {
		return AddResult{Rejected: true, RejectReason: fmt.Sprintf("attachment limit reached (%d images)", s.maxImages)}
	}
	if t.totalBytes+int64(len(data)) > s.maxBytes {
		return AddResult{Rejected: true, RejectReason: fmt.Sprintf("attachment size limit reached (%d bytes)", s.maxBytes)}
	}

	ord := t.nextOrd
	att := &Attachment{
		ID:        fmt.Sprintf("img_%s_%d", hash[:6], ord),
		Ordinal:   ord,
		MediaType: mediaType,
		Data:      data,
		Hash:      hash,
	}
	t.byID[att.ID] = att
	t.byHash[hash] = att
	t.order = append(t.order, att.ID)
	t.totalBytes += int64(len(data))
	t.nextOrd++
	return AddResult{Attachment: att}
}

// Lookup returns the attachment for (convID, id). A miss is expected after
// restart/resume or for an unknown ID and must be handled gracefully by callers.
func (s *Store) Lookup(convID, id string) (*Attachment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.convs[convID]
	if t == nil {
		return nil, false
	}
	att, ok := t.byID[id]
	return att, ok
}

// LookupAny returns a live attachment by ID without requiring the caller to know
// its conversation ID. IDs are designed to be conversation-scoped, not globally
// unique, so this helper only succeeds when there is exactly one live match.
// A zero match returns (nil, "", false, false). Multiple matches return
// (nil, "", false, true) so callers can request an explicit conversation ID
// rather than guessing.
func (s *Store) LookupAny(id string) (att *Attachment, convID string, ok bool, ambiguous bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for cid, t := range s.convs {
		if t == nil {
			continue
		}
		candidate, found := t.byID[id]
		if !found {
			continue
		}
		if ok {
			return nil, "", false, true
		}
		att, convID, ok = candidate, cid, true
	}
	return att, convID, ok, false
}

// Clear drops all attachments for a conversation (e.g. on conversation unload).
func (s *Store) Clear(convID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.convs, convID)
}

// Count returns how many images are stored for a conversation. Test/introspection helper.
func (s *Store) Count(convID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.convs[convID]; t != nil {
		return len(t.order)
	}
	return 0
}
