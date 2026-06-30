package llm

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveImageBytes_Base64(t *testing.T) {
	raw := []byte("PNGDATA")
	b := Block{Type: BlockImage, MediaType: "image/png", ImageData: base64.StdEncoding.EncodeToString(raw)}
	got, err := ResolveImageBytes(context.Background(), b)
	if err != nil || string(got) != "PNGDATA" {
		t.Fatalf("base64 → %q, %v", got, err)
	}
}

func TestResolveImageBytes_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("REMOTEBYTES"))
	}))
	defer srv.Close()
	b := Block{Type: BlockImage, ImageURL: srv.URL}
	got, err := ResolveImageBytes(context.Background(), b)
	if err != nil || string(got) != "REMOTEBYTES" {
		t.Fatalf("url → %q, %v", got, err)
	}
}

func TestResolveImageBytes_BadBase64(t *testing.T) {
	if _, err := ResolveImageBytes(context.Background(), Block{Type: BlockImage, ImageData: "!!!notb64"}); err == nil {
		t.Error("expected error on bad base64")
	}
}

func TestResolveImageBytes_Neither(t *testing.T) {
	if _, err := ResolveImageBytes(context.Background(), Block{Type: BlockImage}); err == nil {
		t.Error("expected error when neither data nor url set")
	}
}
