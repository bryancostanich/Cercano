package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cercano/source/server/internal/enterprise"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func newEnterpriseHost() (*enterprise.Host, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return enterprise.NewHost(enterprise.HostOptions{Directory: filepath.Join(home, ".cercano", "enterprise"), Manager: enterprise.Options{ClientVersion: version}})
}
func openEnterpriseBrowser(ctx context.Context, target string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("enterprise login currently supports macOS")
	}
	return exec.CommandContext(ctx, "open", target).Run()
}

// Connection changes run in the host. This CLI never opens Keychain, rotates a
// refresh token, or treats a downloaded bundle as proof of host enforcement.
func enterpriseCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(out, "Usage: cercano enterprise login --server https://enterprise.example --organization UUID | sync | status | logout | standalone\nConnects to the running local Cercano host. Use --address 127.0.0.1:PORT for a non-default host. Add --json for machine-readable output. Logout keeps managed work blocked; standalone explicitly returns to personal settings.")
		return nil
	}
	switch args[0] {
	case "login", "sync", "status", "logout", "standalone":
	default:
		return errors.New("unknown enterprise command")
	}
	if runtime.GOOS != "darwin" {
		return errors.New("enterprise account management currently supports macOS")
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("load Cercano configuration: %w", err)
	}
	flags := flag.NewFlagSet("enterprise", flag.ContinueOnError)
	flags.SetOutput(out)
	asJSON := flags.Bool("json", false, "print machine-readable status")
	server := flags.String("server", "", "trusted enterprise HTTPS origin")
	org := flags.String("organization", "", "organization ID from your administrator")
	address := flags.String("address", net.JoinHostPort("127.0.0.1", cfg.Port), "running local Cercano host")
	if err = flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected enterprise arguments")
	}
	if args[0] != "login" && (*server != "" || *org != "") {
		return errors.New("server and organization apply only to login")
	}
	host, _, err := net.SplitHostPort(*address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("enterprise controls require a literal loopback host address")
	}
	conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	timeout := 90 * time.Second
	if args[0] == "login" {
		timeout = 6 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := proto.NewEnterpriseClient(conn)
	empty := &proto.EnterpriseControlRequest{}
	var result *proto.EnterpriseStatus
	switch args[0] {
	case "login":
		result, err = client.Login(ctx, &proto.EnterpriseLoginRequest{Server: *server, OrganizationId: *org})
	case "sync":
		result, err = client.Synchronize(ctx, empty)
	case "status":
		result, err = client.GetStatus(ctx, empty)
	case "logout":
		result, err = client.Logout(ctx, empty)
	case "standalone":
		result, err = client.UseStandalone(ctx, empty)
	}
	if status.Code(err) == codes.Unimplemented {
		return errors.New("running host does not support enterprise management; update and restart the host")
	}
	if status.Code(err) == codes.Unavailable {
		return errors.New("cannot reach the Cercano host; start 'cercano agent' and retry")
	}
	if err != nil {
		return err
	}
	return writeEnterpriseStatus(out, result, *asJSON)
}

func writeEnterpriseStatus(out io.Writer, result *proto.EnterpriseStatus, asJSON bool) error {
	if !asJSON {
		_, err := io.WriteString(out, enterpriseStatusText(result))
		return err
	}
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true, Indent: "  "}).Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

// Quote externally supplied strings so names cannot inject terminal controls.
func enterpriseStatusText(s *proto.EnterpriseStatus) string {
	var out strings.Builder
	mode := "Personal settings"
	if s.Managed {
		mode = "Enterprise managed"
	}
	fmt.Fprintf(&out, "Mode: %s\n", mode)
	if !s.Connected {
		if s.Managed {
			out.WriteString("Not signed in. Managed work is blocked. Run 'cercano enterprise login' to reconnect, or 'cercano enterprise standalone' to return to personal settings.\n")
		}
		return out.String()
	}
	if s.MembershipKnown {
		fmt.Fprintf(&out, "Organization: %q (%q)\n", s.OrganizationName, s.OrganizationId)
		if s.TeamId == "" {
			out.WriteString("Team: None assigned (organization policy applies)\n")
		} else {
			fmt.Fprintf(&out, "Team: %q (%q)\n", s.TeamName, s.TeamId)
		}
	} else {
		fmt.Fprintf(&out, "Organization: %q\nTeam: Details unavailable until a supporting server supplies them\n", s.OrganizationId)
	}
	fmt.Fprintf(&out, "Host: %q\n", s.HostId)
	if s.Revision > 0 {
		fmt.Fprintf(&out, "Applied policy revision: %d\nAuthorization valid until: %q\n", s.Revision, s.ValidUntil)
	} else {
		out.WriteString("Policy: Awaiting verified synchronization\n")
	}
	switch {
	case s.Changing:
		out.WriteString("Status: Updating enterprise connection\n")
	case !s.Usable:
		out.WriteString("Status: Managed work is blocked. Run 'cercano enterprise sync'; if access was revoked, contact your administrator.\n")
	case s.Error != "":
		out.WriteString("Status: Using the last verified policy while synchronization recovers\n")
	default:
		out.WriteString("Status: Ready; the running host has applied the policy\n")
	}
	if s.Error != "" {
		fmt.Fprintf(&out, "Last synchronization error: %q\n", s.Error)
	}
	if s.Revision > 0 && (s.Error != "" || !s.Usable) {
		out.WriteString("Organization and team details describe the last applied policy.\n")
	}
	return out.String()
}
