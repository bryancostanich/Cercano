package server

import (
	"bytes"
	"testing"

	"cercano/source/server/internal/visionattach"
	"cercano/source/server/pkg/proto"
)

func TestExportImage_ReturnsLiveAttachmentBytes(t *testing.T) {
	store := visionattach.NewStore()
	data := []byte("fake png bytes")
	added := store.Add("conv1", "image/png", data)
	if added.Attachment == nil || added.Rejected {
		t.Fatalf("Add rejected: %+v", added)
	}
	s := &Server{visionStore: store}

	resp, err := s.ExportImage(t.Context(), &proto.ExportImageRequest{
		ConversationId: "conv1",
		ImageId:        added.Attachment.ID,
	})
	if err != nil {
		t.Fatalf("ExportImage: %v", err)
	}
	if !resp.GetFound() {
		t.Fatal("expected found=true")
	}
	if resp.GetMediaType() != "image/png" {
		t.Fatalf("media type = %q", resp.GetMediaType())
	}
	if !bytes.Equal(resp.GetData(), data) {
		t.Fatalf("data = %q, want %q", resp.GetData(), data)
	}
}

func TestExportImage_BlankConversationIDFindsUniqueImageID(t *testing.T) {
	store := visionattach.NewStore()
	data := []byte("fake png bytes")
	added := store.Add("conv1", "image/png", data)
	s := &Server{visionStore: store}

	resp, err := s.ExportImage(t.Context(), &proto.ExportImageRequest{
		ImageId: added.Attachment.ID,
	})
	if err != nil {
		t.Fatalf("ExportImage: %v", err)
	}
	if !resp.GetFound() || !bytes.Equal(resp.GetData(), data) {
		t.Fatalf("ExportImage blank conv = found:%v data:%q", resp.GetFound(), resp.GetData())
	}
}

func TestExportImage_BlankConversationIDAmbiguous(t *testing.T) {
	store := visionattach.NewStore()
	a := store.Add("convA", "image/png", []byte("same")).Attachment
	b := store.Add("convB", "image/png", []byte("same")).Attachment
	if a.ID != b.ID {
		t.Fatalf("test setup expected duplicate IDs, got %q and %q", a.ID, b.ID)
	}
	s := &Server{visionStore: store}

	_, err := s.ExportImage(t.Context(), &proto.ExportImageRequest{ImageId: a.ID})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestExportImage_MissIsNotError(t *testing.T) {
	s := &Server{visionStore: visionattach.NewStore()}
	resp, err := s.ExportImage(t.Context(), &proto.ExportImageRequest{
		ConversationId: "conv1",
		ImageId:        "img_missing_1",
	})
	if err != nil {
		t.Fatalf("ExportImage: %v", err)
	}
	if resp.GetFound() {
		t.Fatal("expected found=false for missing attachment")
	}
}

func TestExportImage_NilStoreIsMiss(t *testing.T) {
	s := &Server{}
	resp, err := s.ExportImage(t.Context(), &proto.ExportImageRequest{
		ConversationId: "conv1",
		ImageId:        "img_missing_1",
	})
	if err != nil {
		t.Fatalf("ExportImage: %v", err)
	}
	if resp.GetFound() {
		t.Fatal("expected found=false with nil store")
	}
}
