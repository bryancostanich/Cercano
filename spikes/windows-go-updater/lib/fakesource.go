// Package lib contains the experiments against the pinned, real
// github.com/creativeprojects/go-selfupdate API (v1.6.0).
//
// FakeSource is a selfupdate.Source backed entirely by LOCAL fixture files.
// It never performs network I/O: asset "downloads" are os.Open calls on
// files registered at construction, so no remote bytes are ever fetched,
// and nothing but this spike's own fixture binaries is ever executed.
package lib

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

// AssetSpec names one release asset and the LOCAL file that backs it.
type AssetSpec struct {
	Name string // exact asset name, e.g. cercano-0.21.0-windows-x64.zip
	Path string // absolute local path (fixture-generated)
}

// ReleaseSpec describes one fake release.
type ReleaseSpec struct {
	Tag        string // e.g. v0.21.0 (Cercano tags releases as v<version>)
	Draft      bool
	Prerelease bool
	Assets     []AssetSpec
}

// FakeSource implements selfupdate.Source from local files only.
type FakeSource struct {
	mu        sync.Mutex
	releases  []fakeRelease
	assetByID map[int64]fakeAsset
	// Downloads counts DownloadReleaseAsset calls per asset name.
	Downloads map[string]int
	// FailOnNames makes DownloadReleaseAsset fail for matching asset names,
	// to inject failures mid-transaction.
	FailOnNames map[string]bool
}

type fakeAsset struct {
	id   int64
	name string
	path string
	size int
}

type fakeRelease struct {
	tag, name, url, notes string
	draft, prerelease     bool
	assets                []fakeAsset
}

var (
	_ selfupdate.Source        = (*FakeSource)(nil)
	_ selfupdate.SourceRelease = (*fakeRelease)(nil)
	_ selfupdate.SourceAsset   = (*fakeAsset)(nil)
)

// NewFakeSource validates and registers fixture releases. Every asset path
// must exist and be a regular file.
func NewFakeSource(releases []ReleaseSpec) (*FakeSource, error) {
	s := &FakeSource{
		assetByID:   make(map[int64]fakeAsset),
		Downloads:   make(map[string]int),
		FailOnNames: make(map[string]bool),
	}
	var id int64
	for _, rs := range releases {
		fr := fakeRelease{
			tag:        rs.Tag,
			name:       rs.Tag,
			url:        "file://local/" + rs.Tag,
			notes:      "fixture release " + rs.Tag,
			draft:      rs.Draft,
			prerelease: rs.Prerelease,
		}
		for _, a := range rs.Assets {
			st, err := os.Stat(a.Path)
			if err != nil {
				return nil, fmt.Errorf("fixture asset %q: %w", a.Name, err)
			}
			if !st.Mode().IsRegular() {
				return nil, fmt.Errorf("fixture asset %q is not a regular file", a.Name)
			}
			id++
			fa := fakeAsset{id: id, name: a.Name, path: a.Path, size: int(st.Size())}
			fr.assets = append(fr.assets, fa)
			s.assetByID[id] = fa
		}
		s.releases = append(s.releases, fr)
	}
	return s, nil
}

func (s *FakeSource) ListReleases(ctx context.Context, repo selfupdate.Repository) ([]selfupdate.SourceRelease, error) {
	out := make([]selfupdate.SourceRelease, 0, len(s.releases))
	for i := range s.releases {
		out = append(out, &s.releases[i])
	}
	return out, nil
}

// DownloadReleaseAsset opens the registered local file. It refuses to serve
// anything that was not registered at construction, and it never executes
// the bytes it returns.
func (s *FakeSource) DownloadReleaseAsset(ctx context.Context, rel *selfupdate.Release, assetID int64) (io.ReadCloser, error) {
	s.mu.Lock()
	a, ok := s.assetByID[assetID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown asset id %d", assetID)
	}
	// Note: the library passes the SAME *Release (whose AssetName is the
	// archive) when fetching validation assets, so the asset name here can
	// legitimately differ from rel.AssetName; no name check is enforced.
	s.mu.Lock()
	s.Downloads[a.name]++
	failing := s.FailOnNames[a.name]
	s.mu.Unlock()
	if failing {
		return nil, errors.New("injected download failure")
	}
	return os.Open(a.path)
}

func (r *fakeRelease) GetID() int64        { return int64(len(r.tag)) }
func (r *fakeRelease) GetTagName() string  { return r.tag }
func (r *fakeRelease) GetDraft() bool      { return r.draft }
func (r *fakeRelease) GetPrerelease() bool { return r.prerelease }
func (r *fakeRelease) GetPublishedAt() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}
func (r *fakeRelease) GetReleaseNotes() string { return r.notes }
func (r *fakeRelease) GetName() string         { return r.name }
func (r *fakeRelease) GetURL() string          { return r.url }
func (r *fakeRelease) GetAssets() []selfupdate.SourceAsset {
	out := make([]selfupdate.SourceAsset, 0, len(r.assets))
	for i := range r.assets {
		out = append(out, &r.assets[i])
	}
	return out
}

func (a *fakeAsset) GetID() int64                  { return a.id }
func (a *fakeAsset) GetName() string               { return a.name }
func (a *fakeAsset) GetSize() int                  { return a.size }
func (a *fakeAsset) GetBrowserDownloadURL() string { return "file://local/" + a.name }
