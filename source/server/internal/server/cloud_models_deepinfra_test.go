package server

import (
	"cercano/source/server/internal/catalog"
	"cercano/source/server/internal/deepinfracatalog"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestProfileDiscoveryReusesDeepInfraRegistry(t *testing.T) {
	data, err := os.ReadFile("../deepinfracatalog/testdata/models_list.json")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("public catalog received credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer fixture.Close()
	source := deepinfracatalog.New(deepinfracatalog.WithIndexURL(fixture.URL), deepinfracatalog.WithTTL(time.Nanosecond))
	reg := catalog.NewRegistry()
	reg.Register(source)
	s, _ := newTestServer()
	s.SetCatalogRegistry(reg)
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{{Name: "custom-deepinfra", Flavor: "chat_completions", BaseURL: "https://api.deepinfra.com/v1/openai"}}})
	req := &proto.ListCloudProfileModelsRequest{ProfileName: "custom-deepinfra"}
	first, err := s.ListCloudProfileModels(context.Background(), req)
	if err != nil || first.Error != "" || len(first.Models) == 0 || hits.Load() != 1 {
		t.Fatalf("catalog response=%v err=%v hits=%d", first, err, hits.Load())
	}
	fixture.Close()
	stale, err := s.ListCloudProfileModels(context.Background(), req)
	if err != nil || stale.Error != "" || len(stale.Models) != len(first.Models) {
		t.Fatalf("shared stale cache lost: %v %v", stale, err)
	}
}
