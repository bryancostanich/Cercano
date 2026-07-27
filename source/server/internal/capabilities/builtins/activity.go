package builtins

import (
	"fmt"
	"time"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

// activityReporter opens a child "activity" tab in the CLI for a long-running
// capability and streams lifecycle events into it. Any capability with a
// meaningful progress lifecycle should use one instead of only calling
// call.Emit, so parallel invocations get their own readable surface rather than
// blurring together in the main transcript.
//
// The CLI treats any SubAgentID with an "activity:" prefix as an activity tab:
// it opens the tab, renders the prompt/progress/done lines, and attaches an
// "open tab" affordance to the tool row that spawned it (via the tool-use id
// the loop injects into the started event).
type activityReporter struct {
	call  *capabilities.Call
	id    string
	tool  string
	title string
}

// newActivityReporter builds a reporter with a process-unique id. title is the
// tab label (e.g. "research"); tool is the capability name used on events.
func newActivityReporter(call *capabilities.Call, tool, title string) *activityReporter {
	return &activityReporter{
		call:  call,
		id:    fmt.Sprintf("activity:%s:%d", tool, time.Now().UnixNano()),
		tool:  tool,
		title: title,
	}
}

func (r *activityReporter) emit(kind, text string) {
	if r == nil || r.call == nil || r.call.EmitProgress == nil {
		return
	}
	r.call.EmitProgress(agenttools.ProgressEvent{
		Text:             text,
		SubAgentID:       r.id,
		SubAgentParentID: r.call.ConversationID,
		SubAgentTitle:    r.title,
		Kind:             kind,
		ToolName:         r.tool,
		Summary:          text,
		IsError:          kind == "error",
	})
}

// Started opens the activity tab. summary is the one-line lifecycle header.
func (r *activityReporter) Started(summary string) { r.emit("started", summary) }

// Prompt records the task/query the activity is working on, shown as the tab's
// leading user entry.
func (r *activityReporter) Prompt(text string) { r.emit("prompt", text) }

// Progress appends a formatted progress line to the tab.
func (r *activityReporter) Progress(text string) { r.emit("progress", text) }

// Done marks successful completion.
func (r *activityReporter) Done(summary string) { r.emit("done", summary) }

// Failed marks an error; the tab is preserved for post-mortem review.
func (r *activityReporter) Failed(err error) {
	if err == nil {
		return
	}
	r.emit("error", r.tool+" failed: "+err.Error())
}
