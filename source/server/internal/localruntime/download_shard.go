package localruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// shardOutcome reports how a single shard's download ended. Only shardOK
// continues the download loop; the cancelled and failed paths have already
// recorded the model's terminal state via markDownloadCancelled / failDownload.
type shardOutcome int

const (
	shardOK shardOutcome = iota
	shardCancelled
	shardFailed
)

// urlBase returns the filename portion of a download URL (the segment after
// the last slash), used to place each shard on disk under its own name.
func urlBase(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// shardTargets returns the on-disk destination for each of a model's files, in
// shard order. A single-file model yields just Path; a multi-shard model
// yields one path per URL, all in Path's directory with the filename taken
// from the URL. The length always matches the model's download URL list.
func shardTargets(model ModelRecord) []string {
	if len(model.DownloadURLs) <= 1 {
		return []string{model.Path}
	}
	dir := filepath.Dir(model.Path)
	out := make([]string, 0, len(model.DownloadURLs))
	for _, u := range model.DownloadURLs {
		out = append(out, filepath.Join(dir, urlBase(u)))
	}
	return out
}

// downloadShard fetches one file to destPath with .part resume and cancel
// support, reporting cumulative progress as baseOffset plus this shard's
// bytes. On cancel or failure it records the model's terminal state and
// returns the matching outcome so the caller stops the loop; on success it
// returns the shard's byte count and shardOK. It is the per-file primitive
// behind runDownload (see manager.go).
func (m *InMemoryManager) downloadShard(ctx context.Context, url, destPath string, model *ModelRecord, baseOffset int64) (int64, shardOutcome) {
	tempPath := destPath + ".part"
	// Resume support: a failed attempt's partial survives, so a retry picks up
	// where it left off via a Range request instead of re-transferring
	// gigabytes. Servers that ignore Range reply 200 with the full body —
	// handled by starting over.
	var resumeFrom int64
	if fi, statErr := os.Stat(tempPath); statErr == nil && fi.Size() > 0 {
		resumeFrom = fi.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		m.failDownload(*model, err)
		return 0, shardFailed
	}
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.markDownloadCancelled(*model)
			return 0, shardCancelled
		}
		m.failDownload(*model, err)
		return 0, shardFailed
	}
	defer resp.Body.Close()
	switch {
	case resumeFrom > 0 && resp.StatusCode == http.StatusPartialContent:
		m.WriteLog(LogEntry{
			Source:  "cercano.runtime.download",
			Level:   "info",
			ModelID: model.ID,
			Message: fmt.Sprintf("resuming %s from %d bytes", urlBase(url), resumeFrom),
		})
	case resp.StatusCode == http.StatusOK:
		// Fresh download — or the server ignored our Range header and sent the
		// full body, in which case the partial is discarded and we start over.
		resumeFrom = 0
	default:
		m.failDownload(*model, fmt.Errorf("download returned HTTP %d", resp.StatusCode))
		return 0, shardFailed
	}
	var file *os.File
	if resumeFrom > 0 {
		file, err = os.OpenFile(tempPath, os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		file, err = os.Create(tempPath)
	}
	if err != nil {
		m.failDownload(*model, err)
		return 0, shardFailed
	}
	written := resumeFrom
	model.DownloadedBytes = baseOffset + written
	buf := make([]byte, 256*1024)
	lastUpdate := time.Now()
	for {
		if ctx.Err() != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			m.markDownloadCancelled(*model)
			return 0, shardCancelled
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := file.Write(buf[:n]); werr != nil {
				_ = file.Close()
				// Keep the partial — the next attempt resumes from it.
				m.failDownload(*model, werr)
				return 0, shardFailed
			}
			written += int64(n)
			model.DownloadedBytes = baseOffset + written
			if time.Since(lastUpdate) >= 250*time.Millisecond {
				m.updateDownload(*model)
				lastUpdate = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			if errors.Is(readErr, context.Canceled) || ctx.Err() != nil {
				// Deliberate cancel discards the partial.
				_ = os.Remove(tempPath)
				m.markDownloadCancelled(*model)
				return 0, shardCancelled
			}
			// Keep the partial — the next attempt resumes from it.
			m.failDownload(*model, readErr)
			return 0, shardFailed
		}
	}
	if cerr := file.Close(); cerr != nil {
		_ = os.Remove(tempPath)
		m.failDownload(*model, cerr)
		return 0, shardFailed
	}
	if rerr := os.Rename(tempPath, destPath); rerr != nil {
		_ = os.Remove(tempPath)
		m.failDownload(*model, rerr)
		return 0, shardFailed
	}
	return written, shardOK
}
