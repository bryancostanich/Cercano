package main

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestUpgradeRestartCommand(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		args                       []string
		cfgErr, exeErr, restartErr bool
		restarted                  bool
		wantCode, wantCalls        int
		message                    string
	}{
		{name: "help", args: []string{"--help"}, wantCode: 0, message: "Usage:"},
		{name: "invalid port", args: []string{"--address", "127.0.0.1:0"}, wantCode: 2},
		{name: "remote endpoint", args: []string{"--address", "192.0.2.1:9000"}, wantCode: 2},
		{name: "unbounded timeout", args: []string{"--timeout", "0s"}, wantCode: 2},
		{name: "extra arguments", args: []string{"surprise"}, wantCode: 2},
		{name: "configuration error", cfgErr: true, wantCode: 1},
		{name: "executable error", exeErr: true, wantCode: 1},
		{name: "absent", wantCode: 0, wantCalls: 1, message: "nothing started"},
		{name: "restarted", restarted: true, wantCode: 0, wantCalls: 1, message: "restarted using"},
		{name: "restart failure", restartErr: true, wantCode: 1, wantCalls: 1, message: "update may already be installed"},
		{name: "explicit address bypasses config", args: []string{"--address", "127.0.0.1:4242"}, cfgErr: true, wantCode: 0, wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			calls := 0
			cfgCalls := 0
			code := upgradeRestartCommand(tt.args, &output,
				func() (string, error) {
					cfgCalls++
					if tt.cfgErr {
						return "", errors.New("config error")
					}
					return "4242", nil
				},
				func() (string, error) {
					if tt.exeErr {
						return "", errors.New("exe error")
					}
					return "/fake/Cellar/cercano/2/bin/cercano", nil
				},
				func(ctx context.Context, path string, endpoint netip.AddrPort) (bool, error) {
					calls++
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("no restart deadline")
					}
					if path != "/fake/Cellar/cercano/2/bin/cercano" || endpoint.String() != "127.0.0.1:4242" {
						t.Fatal("incorrect restart target")
					}
					if tt.restartErr {
						return false, errors.New("failure")
					}
					return tt.restarted, nil
				})
			if code != tt.wantCode || calls != tt.wantCalls {
				t.Fatalf("code=%d calls=%d output=%s", code, calls, output.String())
			}
			if !strings.Contains(output.String(), tt.message) {
				t.Fatalf("missing output %q: %s", tt.message, output.String())
			}
			if (tt.name == "help" || tt.name == "explicit address bypasses config") && cfgCalls != 0 {
				t.Fatal("unexpected config access")
			}
		})
	}
}
