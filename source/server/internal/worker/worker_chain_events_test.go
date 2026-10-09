package worker

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	pkgcfg "cercano/source/server/pkg/config"
)

// TestWorkerChainEvents captures and validates the log output from workerChainEvents
// to ensure all required fields are included in the diagnostic output.
func TestWorkerChainEvents_LogOutput(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer

	// Temporarily replace the global logger output
	originalOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalOutput)

	// Create a test event with all fields populated
	testEvent := resilience.Event{
		Action:           resilience.ActionRetry,
		Stage:            "stream_dial",
		Class:            llm.ErrNetwork,
		From:             "primary",
		To:               "secondary",
		Wait:             5 * time.Second,
		Err:              &testError{"connection failed"},
		Reason:           "quota_exceeded,busy",
		ConversationID:   "conv-123",
		RequestID:        "req-456",
		Emitted:          true,
		EmittedText:      true,
		EmittedReasoning: false,
		EmittedToolCall:  true,
	}

	// Call workerChainEvents with a test destination
	destination := pkgcfg.DestinationPrimary
	eventHandler := workerChainEvents(destination)
	eventHandler(testEvent)

	// Get the captured log output
	logOutput := buf.String()

	// Verify all required fields are present in the log
	requiredFields := []string{
		"[worker] " + string(destination) + " resilience retry",
		"(stream_dial, network)",
		"reason=quota_exceeded,busy",
		"conv=conv-123",
		"req=req-456",
		"emitted=true",
		"text=true",
		"reasoning=false",
		"tool=true",
		// Emitted=true means visible content was interrupted mid-answer: the
		// notice must announce the same-model restart, not a plain retry.
		"primary unreachable mid-answer — restarting the reply",
		"connection failed",
	}

	for _, field := range requiredFields {
		if !strings.Contains(logOutput, field) {
			t.Errorf("Log output missing required field: %s\nFull log output: %s", field, logOutput)
		}
	}
}

// TestWorkerChainEvents_FreshRetryNotice keeps the pre-content retry wording:
// with nothing emitted yet, the same-provider retry is announced plainly.
func TestWorkerChainEvents_FreshRetryNotice(t *testing.T) {
	var buf bytes.Buffer

	originalOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalOutput)

	event := resilience.Event{
		Action: resilience.ActionRetry,
		Stage:  "stream_dial",
		Class:  llm.ErrNetwork,
		From:   "primary",
		Emitted: false,
		Err:    &testError{"connection failed"},
	}

	workerChainEvents(pkgcfg.DestinationPrimary)(event)

	logOutput := buf.String()
	for _, field := range []string{"primary unreachable — trying once more", "connection failed"} {
		if !strings.Contains(logOutput, field) {
			t.Errorf("Log output missing required field: %s\nFull log output: %s", field, logOutput)
		}
	}
}

// TestWorkerChainEvents_EmittedFlags tests the emitted flags specifically
func TestWorkerChainEvents_EmittedFlags(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer

	// Temporarily replace the global logger output
	originalOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalOutput)

	// Test cases for different emitted flag combinations
	testCases := []struct {
		name     string
		event    resilience.Event
		expected string
	}{
		{
			name: "all emitted flags true",
			event: resilience.Event{
				Action:           resilience.ActionSurface,
				Stage:            "chat",
				Class:            llm.ErrQuota,
				Emitted:          true,
				EmittedText:      true,
				EmittedReasoning: true,
				EmittedToolCall:  true,
				ConversationID:   "conv-789",
				RequestID:        "req-101",
			},
			expected: "emitted=true text=true reasoning=true tool=true",
		},
		{
			name: "no emitted flags",
			event: resilience.Event{
				Action:           resilience.ActionRetry,
				Stage:            "stream_first",
				Class:            llm.ErrBusy,
				Emitted:          false,
				EmittedText:      false,
				EmittedReasoning: false,
				EmittedToolCall:  false,
				ConversationID:   "conv-202",
				RequestID:        "req-303",
			},
			expected: "emitted=false text=false reasoning=false tool=false",
		},
		{
			name: "partial emitted flags",
			event: resilience.Event{
				Action:           resilience.ActionFailover,
				Stage:            "stream_live",
				Class:            llm.ErrAuth,
				Emitted:          true,
				EmittedText:      false,
				EmittedReasoning: true,
				EmittedToolCall:  false,
				ConversationID:   "conv-404",
				RequestID:        "req-505",
			},
			expected: "emitted=true text=false reasoning=true tool=false",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset() // Clear buffer for each test

			destination := pkgcfg.DestinationSecondary
			eventHandler := workerChainEvents(destination)
			eventHandler(tc.event)

			logOutput := buf.String()

			if !strings.Contains(logOutput, tc.expected) {
				t.Errorf("Expected %s in log output, got: %s", tc.expected, logOutput)
			}
		})
	}
}

// TestWorkerChainEvents_RequestID_ConversationID tests correlation fields
func TestWorkerChainEvents_RequestID_ConversationID(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer

	// Temporarily replace the global logger output
	originalOutput := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(originalOutput)

	testCases := []struct {
		name             string
		convID           string
		reqID            string
		shouldContain    []string
		shouldNotContain []string
	}{
		{
			name:          "both IDs present",
			convID:        "conversation-123",
			reqID:         "request-456",
			shouldContain: []string{"conv=conversation-123", "req=request-456"},
		},
		{
			name:             "only conversation ID",
			convID:           "conversation-789",
			reqID:            "",
			shouldContain:    []string{"conv=conversation-789"},
			shouldNotContain: []string{"req=request"},
		},
		{
			name:             "only request ID",
			convID:           "",
			reqID:            "request-101",
			shouldContain:    []string{"req=request-101"},
			shouldNotContain: []string{"conv=conversation"},
		},
		{
			name:          "no IDs",
			convID:        "",
			reqID:         "",
			shouldContain: []string{"conv=", "req="},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset() // Clear buffer for each test

			event := resilience.Event{
				Action:         resilience.ActionRetry,
				Stage:          "stream_dial",
				Class:          llm.ErrNetwork,
				ConversationID: tc.convID,
				RequestID:      tc.reqID,
			}

			destination := pkgcfg.DestinationPrimary
			eventHandler := workerChainEvents(destination)
			eventHandler(event)

			logOutput := buf.String()

			for _, shouldContain := range tc.shouldContain {
				if !strings.Contains(logOutput, shouldContain) {
					t.Errorf("Expected log to contain %s, got: %s", shouldContain, logOutput)
				}
			}

			for _, shouldNotContain := range tc.shouldNotContain {
				if strings.Contains(logOutput, shouldNotContain) {
					t.Errorf("Expected log to NOT contain %s, got: %s", shouldNotContain, logOutput)
				}
			}
		})
	}
}

// testError is a simple error type for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
