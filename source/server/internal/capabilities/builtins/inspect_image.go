package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// inspectImageCap lets a text reasoning model ask a focused visual question
// about an image it cannot see directly. Images the user attaches are stored by
// a conversation-scoped ID (see internal/visionattach) and the reasoning model
// receives a text placeholder naming that ID (see agent.RewriteImagesToPlaceholders)
// instead of the raw pixels. inspect_image is how the model turns that ID back
// into an answer: it resolves the attachment and routes a single, tool-less
// question to the configured vision-tier model.
//
// This is the tool skeleton: it validates args, gates on vision availability,
// and handles unknown/stale image IDs with a clear reattach message. The real
// vision provider call is performed by the wired VisionService.Inspect.
type inspectImageCap struct{}

// InspectImage constructs the inspect_image capability.
func InspectImage() capabilities.Capability { return inspectImageCap{} }

func (inspectImageCap) Name() string            { return "inspect_image" }
func (inspectImageCap) Tier() capabilities.Tier { return capabilities.TierR }
func (inspectImageCap) Surfaces() capabilities.Surface {
	// Agent-only: it depends on the per-conversation attachment store, which is
	// a live agent-session concept. The MCP host has no such store.
	return capabilities.SurfaceAgent
}
func (inspectImageCap) Description() string {
	return "Ask a focused visual question about an image the user attached to this conversation. You cannot see attached images directly — each is referenced by a stable image_id in a placeholder. Call inspect_image with that id and a specific question (e.g. \"what error is shown in this screenshot?\"). Args: {image_id: string, question: string}."
}
func (inspectImageCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["image_id", "question"],
		"properties": {
			"image_id": {"type": "string", "description": "The image id from the attachment placeholder, e.g. img_7f3a9c_1."},
			"question": {"type": "string", "description": "A specific question about the image."}
		}
	}`)
}

type inspectImageArgs struct {
	ImageID  string `json:"image_id"`
	Question string `json:"question"`
}

func (inspectImageCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a inspectImageArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("inspect_image: parse args: %w", err)
	}
	a.ImageID = strings.TrimSpace(a.ImageID)
	a.Question = strings.TrimSpace(a.Question)
	if a.ImageID == "" {
		return nil, errors.New("inspect_image: image_id is required")
	}
	if a.Question == "" {
		return nil, errors.New("inspect_image: question is required")
	}

	vs := call.Svc.Vision
	// No vision service wired, or no vision model configured/reachable for this
	// locus: report unavailable as a normal tool result, not a hard error, so
	// the reasoning turn continues.
	if vs == nil || !vs.Available() {
		return capabilities.NewTextResult(
			"Vision is not available: no vision model is configured for the current setup. " +
				"Ask the user to configure a vision-tier model (models.open.overrides.<runtime>.vision) " +
				"or describe the image in text.",
		), nil
	}

	// Stale/unknown image ID. Attachments are not persisted, so a resumed
	// conversation or a wrong id lands here. Tell the model plainly to have the
	// user reattach rather than crash or invent an answer.
	if !vs.Lookup(call.ConversationID, a.ImageID) {
		return capabilities.NewTextResult(fmt.Sprintf(
			"Image %s is no longer available in memory. Ask the user to reattach the image before inspecting it.",
			a.ImageID,
		)), nil
	}

	ans, err := vs.Inspect(ctx, call.ConversationID, a.ImageID, a.Question)
	if err != nil {
		// Surface provider failures as a tool result, not a turn-aborting error:
		// the reasoning model can retry, ask a different question, or fall back
		// to asking the user.
		return capabilities.NewTextResult(fmt.Sprintf(
			"Could not inspect image %s: %v", a.ImageID, err,
		)), nil
	}

	return capabilities.NewTextResult(renderVisionEnvelope(a.ImageID, a.Question, ans)), nil
}

// renderVisionEnvelope formats the stable text envelope inspect_image returns.
// Keeping the envelope stable (image id, question, answer, then optional
// confidence/source) gives the reasoning model a predictable shape to read.
func renderVisionEnvelope(imageID, question string, ans capabilities.VisionAnswer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Image %s inspection result:\n", imageID)
	fmt.Fprintf(&b, "Question: %s\n", question)
	fmt.Fprintf(&b, "Answer: %s", strings.TrimSpace(ans.Answer))
	if c := strings.TrimSpace(ans.Confidence); c != "" {
		fmt.Fprintf(&b, "\nConfidence: %s", c)
	}
	if s := strings.TrimSpace(ans.Source); s != "" {
		fmt.Fprintf(&b, "\nSource: %s", s)
	}
	return b.String()
}
