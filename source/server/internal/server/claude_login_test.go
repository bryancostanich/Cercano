package server

import "testing"

func TestShouldActivateClaudeLogin(t *testing.T) {
	cases := []struct {
		name             string
		setActive        bool
		canonicalProfile bool
		want             bool
	}{
		{
			name:             "canonical sign-in activates even without explicit flag",
			canonicalProfile: true,
			want:             true,
		},
		{
			name:      "explicit set-active activates named profile",
			setActive: true,
			want:      true,
		},
		{
			name: "named reauth without set-active does not activate",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldActivateClaudeLogin(tc.setActive, tc.canonicalProfile); got != tc.want {
				t.Fatalf("shouldActivateClaudeLogin(%v, %v) = %v, want %v", tc.setActive, tc.canonicalProfile, got, tc.want)
			}
		})
	}
}
