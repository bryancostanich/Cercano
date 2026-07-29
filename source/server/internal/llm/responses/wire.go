package responses

import "encoding/json"

// ---- request ----

type request struct {
	Model           string      `json:"model"`
	Instructions    string      `json:"instructions,omitempty"`
	Input           []inputItem `json:"input"`
	Tools           []tool      `json:"tools,omitempty"`
	MaxOutputTokens int         `json:"max_output_tokens,omitempty"`
	Temperature     *float64    `json:"temperature,omitempty"`
	Store           bool        `json:"store"`
	Include         []string    `json:"include,omitempty"`
	Stream          bool        `json:"stream,omitempty"`
}

type inputItem struct {
	Type string `json:"type"` // message | function_call | function_call_output | reasoning

	// message
	Role    string        `json:"role,omitempty"`
	Content []contentPart `json:"content,omitempty"`

	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	// function_call_output. Output is required on every function_call_output
	// item ("Missing required parameter: 'input[N].output'"), including when the
	// tool produced no text. RawMessage lets the adapter emit an explicit JSON
	// string (even "") for tool results while keeping the field absent (nil) for
	// message/function_call/reasoning items that share this struct.
	Output json.RawMessage `json:"output,omitempty"`

	// reasoning
	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
	// Summary is required on replayed reasoning items ("Missing required
	// parameter: 'input[N].summary'"); we don't retain the model's summary
	// parts, so the adapter sends an empty array. RawMessage keeps the
	// field absent (nil) for non-reasoning item types.
	Summary json.RawMessage `json:"summary,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"` // input_text | input_image
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type tool struct {
	Type        string          `json:"type"` // function
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ---- response (non-stream) ----

type response struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Model  string       `json:"model,omitempty"` // the model that served the call
	Output []outputItem `json:"output"`
	Usage  *usage       `json:"usage,omitempty"`
	Error  *apiError    `json:"error,omitempty"`
}

type outputItem struct {
	Type    string          `json:"type"` // message | function_call | reasoning
	Role    string          `json:"role,omitempty"`
	Content []outputContent `json:"content,omitempty"`

	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`

	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

type outputContent struct {
	Type string `json:"type"` // output_text
	Text string `json:"text,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}
