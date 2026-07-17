package main

import (
	"testing"

	"cercano/source/server/pkg/config"
)

func TestShouldWarnCloudRebuildFailure(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "cloud primary without active profile warns",
			cfg:  config.Config{LocusMode: "cloud_primary"},
			want: true,
		},
		{
			name: "cloud only without active profile warns",
			cfg:  config.Config{LocusMode: "cloud_only"},
			want: true,
		},
		{
			name: "empty locus defaults to cloud primary and warns",
			cfg:  config.Config{},
			want: true,
		},
		{
			name: "open only without active profile is intentionally local",
			cfg:  config.Config{LocusMode: "open_only"},
			want: false,
		},
		{
			name: "open primary without active profile keeps local primary quiet",
			cfg:  config.Config{LocusMode: "open_primary"},
			want: false,
		},
		{
			name: "active profile exists so rebuild failure is actionable even in open only",
			cfg:  config.Config{LocusMode: "open_only", ActiveCloudProfile: "anthropic"},
			want: true,
		},
		{
			name: "invalid locus is suspicious and should warn",
			cfg:  config.Config{LocusMode: "bogus"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldWarnCloudRebuildFailure(tt.cfg); got != tt.want {
				t.Fatalf("shouldWarnCloudRebuildFailure(%+v) = %v, want %v", tt.cfg, got, tt.want)
			}
		})
	}
}
