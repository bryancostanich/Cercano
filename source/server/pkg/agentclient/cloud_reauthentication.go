package agentclient

import (
	"context"
	"errors"
	"io"

	"cercano/source/server/pkg/proto"
)

type CloudLoginMsg struct {
	ProfileName, Provider, AttemptID        string
	AuthorizeURL, VerificationURL, UserCode string
	Done, Ok                                bool
	Error                                   string
	Err                                     error
}

// ReauthenticateCloud never falls back to an onboarding RPC. Unsupported
// servers surface Unimplemented rather than risking a configuration rewrite.
func (c *Client) ReauthenticateCloud(ctx context.Context, profile, attemptID string) (<-chan CloudLoginMsg, error) {
	rpcCtx, cancel := context.WithCancel(ctx)
	stream, err := c.agent.ReauthenticateCloud(rpcCtx, &proto.CloudReauthenticationRequest{ProfileName: profile, AttemptId: attemptID})
	if err != nil {
		cancel()
		return nil, err
	}
	out := make(chan CloudLoginMsg, 8)
	go func() {
		defer close(out)
		defer cancel()
		send := func(m CloudLoginMsg) bool {
			select {
			case out <- m:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for {
			ev, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				send(CloudLoginMsg{AttemptID: attemptID, ProfileName: profile, Err: err})
				return
			}
			if ev.GetProfileName() != profile || ev.GetAttemptId() != attemptID {
				send(CloudLoginMsg{ProfileName: profile, AttemptID: attemptID, Err: errors.New("reauthentication response identity mismatch")})
				return
			}
			if !send(CloudLoginMsg{ProfileName: ev.GetProfileName(), Provider: ev.GetProvider(), AttemptID: ev.GetAttemptId(), AuthorizeURL: ev.GetAuthorizeUrl(), VerificationURL: ev.GetVerificationUrl(), UserCode: ev.GetUserCode(), Done: ev.GetDone(), Ok: ev.GetOk(), Error: ev.GetError()}) {
				return
			}
			if ev.GetDone() {
				return
			}
		}
	}()
	return out, nil
}
