package ui

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

const maxDroppedImageBytes = 20 << 20 // 20 MiB, mirrors server llm.maxImageBytes

// acceptedImageTypes maps sniffed/looked-up media types we accept.
var acceptedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

type droppedImage struct {
	data      []byte
	mediaType string
	source    string
}

// parseImagePaths splits pasted text into candidate file paths, handling
// single/double-quoted paths, backslash-escaped spaces, and whitespace/newline
// separation between multiple dropped files.
func parseImagePaths(pasted string) []string {
	s := strings.TrimSpace(pasted)
	if s == "" {
		return nil
	}
	var paths []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			paths = append(paths, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i]) // unescape (e.g. "\ " -> " ")
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return paths
}

// loadDroppedImage validates and reads a single image file.
func loadDroppedImage(path string) ([]byte, string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if fi.IsDir() {
		return nil, "", fmt.Errorf("%s is a directory", path)
	}
	if fi.Size() > maxDroppedImageBytes {
		return nil, "", fmt.Errorf("image %s exceeds %d bytes", path, maxDroppedImageBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	mt := sniffImageType(data)
	if !acceptedImageTypes[mt] {
		return nil, "", fmt.Errorf("%s is not an accepted image type", path)
	}
	return data, mt, nil
}

// sniffImageType returns the media type from the content, normalizing the few we
// accept. http.DetectContentType covers png/jpeg/gif/webp.
func sniffImageType(data []byte) string {
	mt := http.DetectContentType(data)
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = mt[:i]
	}
	return strings.TrimSpace(mt)
}

// classifyImagePaste reports whether the whole pasted text resolves to one or
// more accepted image files. ok=false means the caller should treat the paste as
// literal text.
func classifyImagePaste(pasted string) ([]droppedImage, bool) {
	paths := parseImagePaths(pasted)
	if len(paths) == 0 {
		return nil, false
	}
	var out []droppedImage
	for _, p := range paths {
		data, mt, err := loadDroppedImage(p)
		if err != nil {
			return nil, false // any non-image candidate → whole paste is literal
		}
		out = append(out, droppedImage{data: data, mediaType: mt, source: p})
	}
	return out, true
}
