// Package deepinfracatalog adapts DeepInfra's public model index to
// catalog.Source.
//
// It is the first servable source: its models are run on DeepInfra's
// hardware, never downloaded, so it deliberately does not implement
// catalog.Downloadable. Routing one of its models into the download manager
// is therefore a compile-time impossibility rather than a runtime error.
//
// The index is DeepInfra's unauthenticated /models/list. That endpoint is
// unversioned, so parsing here is defensive: unknown fields are ignored,
// absent fields decode to their zero value, and every field the catalog cares
// about is optional on the wire (pointers where "absent" and "zero" differ).
package deepinfracatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"cercano/source/server/internal/catalog"
)

// SourceName is the catalog source identifier for DeepInfra.
const SourceName = "deepinfra"

// defaultIndexURL is DeepInfra's public model index. No auth: the catalog is
// public, and browsing must work before the user has entered a key.
const defaultIndexURL = "https://api.deepinfra.com/models/list"

// defaultTTL bounds how long a fetched index is reused. The index changes on
// the order of days (new model launches), so minutes of staleness costs
// nothing and spares every browse a ~1s round trip.
const defaultTTL = 15 * time.Minute

// defaultTimeout bounds a single index fetch.
//
// Browse already tolerates a source that *fails* — the server logs and omits
// it. What it cannot absorb is a source that is merely *slow*: without a
// bound of its own, a stalled fetch holds the whole browse response until the
// caller's context expires. This converts that stall into a fast failure,
// which the existing degradation path then handles. Observed live latency for
// this endpoint is ~1s for ~400KB, so 10s is roughly a 10x margin.
const defaultTimeout = 10 * time.Second

// toolTag marks a model that can call tools. Cercano's agent loop is built
// entirely on tool calls, so a model without this is not merely
// less capable here — it cannot function at all.
const toolTag = "tools"

// multimodalTag marks a model DeepInfra serves with image input.
//
// The index publishes no explicit supports_vision boolean, so this tag is the
// only affirmative image-capability evidence it offers. It is read in one
// direction only: present means supported, absent means UNKNOWN — never
// "unsupported". An untagged model may still accept images; we simply have no
// evidence, and callers that would send an image must treat missing evidence
// as a refusal rather than a denial of the capability.
const multimodalTag = "multimodal"

// wireModel is one entry of /models/list.
//
// Only the fields the catalog maps are declared; the endpoint sends many more
// (cover art, benchmark scores, descriptions) and unknown fields are ignored
// by encoding/json, so an upstream addition cannot break decoding.
//
// Deprecated and ReplacedBy are pointers because absent and zero must be
// distinguished: the endpoint sends `null` for a live model and a unix
// timestamp for a retired one.
type wireModel struct {
	ModelName    string    `json:"model_name"`
	Type         string    `json:"type"`
	Tags         []string  `json:"tags"`
	MaxTokens    int       `json:"max_tokens"`
	Quantization string    `json:"quantization"`
	Deprecated   *int64    `json:"deprecated"`
	ReplacedBy   string    `json:"replaced_by"`
	Pricing      wirePrice `json:"pricing"`
}

// wirePrice is the pricing block. DeepInfra quotes several units across model
// kinds — per-token, per-image, per-second — discriminated by Type. Only
// "tokens" is meaningful for text generation; the rest are left unmapped
// rather than coerced into a per-token number that would be a lie.
type wirePrice struct {
	Type            string   `json:"type"`
	CentsPerInToken *float64 `json:"cents_per_input_token"`
	CentsPerOutToke *float64 `json:"cents_per_output_token"`
}

// centsPerTokenToMicroUSDPerMillion converts DeepInfra's quoted price into the
// catalog's unit.
//
//	cents/token -> micro-USD/million tokens
//	  /100        cents to USD
//	  *1e6        per token to per million tokens
//	  *1e6        USD to micro-USD
//	  = *1e10 overall
//
// The multiply happens in float because that is how the value arrives, but
// the result is *rounded*, not truncated. That matters: 0.0003 * 1e10
// evaluates to 2999999.9999999995 in IEEE-754, which truncation would turn
// into 2999999 — an off-by-one that then multiplies across token counts.
// Rounding lands it on the intended 3000000.
func centsPerTokenToMicroUSDPerMillion(cents float64) int64 {
	if cents <= 0 || math.IsNaN(cents) || math.IsInf(cents, 0) {
		return 0
	}
	return int64(math.Round(cents * 1e10))
}

// eligible reports whether an index entry is a model this agent can actually
// drive, and is the single gate applied to the index.
//
// Three independent reasons to exclude, all structural rather than matters of
// taste:
//
//   - Not text generation. The index is a whole-product catalog: image, video,
//     speech, embedding, and reranker models share it. None can serve a chat
//     turn.
//   - No tool support. The agent loop is tool calls; a model without them
//     cannot participate.
//   - Retired. DeepInfra keeps deprecated models listed with a timestamp.
//     Offering one to pick would be offering a known dead end.
//
// Of ~371 indexed models this admits ~94 — the filter is doing real work, and
// its absence would bury the usable models in five-to-one noise.
func eligible(m wireModel) bool {
	if m.Type != string(catalog.KindTextGeneration) {
		return false
	}
	if m.Deprecated != nil && *m.Deprecated != 0 {
		return false
	}
	return hasTag(m.Tags, toolTag)
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// publisherOf splits the org prefix off a "Org/Model" id. DeepInfra ids are
// namespaced by publisher, so the publisher need not be fetched separately.
func publisherOf(id string) string {
	if i := strings.Index(id, "/"); i > 0 {
		return id[:i]
	}
	return ""
}

// toModel maps an eligible index entry to a catalog list row.
func toModel(m wireModel) catalog.Model {
	out := catalog.Model{
		Source:        SourceName,
		ID:            m.ModelName,
		Publisher:     publisherOf(m.ModelName),
		Kind:          catalog.KindTextGeneration,
		ContextLength: m.MaxTokens,
		SupportsTools: true, // eligible() admitted it only if tagged
		// Affirmative only: see multimodalTag. False here means "no evidence",
		// which callers must not read as proof of absence.
		SupportsVision: hasTag(m.Tags, multimodalTag),
		ReplacedBy:     m.ReplacedBy,
	}
	if m.Deprecated != nil && *m.Deprecated != 0 {
		out.Deprecated = true
	}
	if m.Pricing.Type == "tokens" {
		if m.Pricing.CentsPerInToken != nil {
			out.PriceIn = centsPerTokenToMicroUSDPerMillion(*m.Pricing.CentsPerInToken)
		}
		if m.Pricing.CentsPerOutToke != nil {
			out.PriceOut = centsPerTokenToMicroUSDPerMillion(*m.Pricing.CentsPerOutToke)
		}
	}
	return out
}

// toDetail maps an index entry to per-model detail.
//
// A hosted model has exactly one served flavor, so it yields a single Variant
// carrying the quantization DeepInfra runs and the price — the shape a
// downloadable source uses for a quant file, minus the size (there is no file
// on disk to have one).
func toDetail(m wireModel) catalog.Detail {
	row := toModel(m)
	return catalog.Detail{
		Source:         SourceName,
		ID:             m.ModelName,
		ContextLength:  m.MaxTokens,
		SupportsTools:  row.SupportsTools,
		SupportsVision: row.SupportsVision,
		Deprecated:     row.Deprecated,
		ReplacedBy:     m.ReplacedBy,
		Variants: []catalog.Variant{{
			Name:         m.ModelName,
			Quantization: m.Quantization,
			PriceIn:      row.PriceIn,
			PriceOut:     row.PriceOut,
		}},
	}
}

// Source adapts DeepInfra's index to catalog.Source.
//
// Note the absence of a ResolveDownload method: this type does not and cannot
// implement catalog.Downloadable, which is what stops a servable model from
// reaching the download manager.
type Source struct {
	indexURL string
	client   *http.Client
	ttl      time.Duration
	timeout  time.Duration

	cache *indexCache
}

var _ catalog.Source = (*Source)(nil)

// Option customizes a Source.
type Option func(*Source)

// WithHTTPClient overrides the HTTP client (tests point this at a fixture
// server).
func WithHTTPClient(c *http.Client) Option {
	return func(s *Source) { s.client = c }
}

// WithIndexURL overrides the index endpoint.
func WithIndexURL(u string) Option {
	return func(s *Source) { s.indexURL = u }
}

// WithTTL overrides how long a fetched index is reused.
func WithTTL(d time.Duration) Option {
	return func(s *Source) { s.ttl = d }
}

// WithTimeout overrides the per-fetch timeout.
func WithTimeout(d time.Duration) Option {
	return func(s *Source) { s.timeout = d }
}

// New returns a DeepInfra catalog source.
func New(opts ...Option) *Source {
	s := &Source{
		indexURL: defaultIndexURL,
		client:   http.DefaultClient,
		ttl:      defaultTTL,
		timeout:  defaultTimeout,
	}
	for _, o := range opts {
		o(s)
	}
	s.cache = newIndexCache()
	return s
}

// Name implements catalog.Source.
func (s *Source) Name() string { return SourceName }

// List implements catalog.Source: the eligible slice of the index, optionally
// narrowed by a substring query on the model id.
func (s *Source) List(ctx context.Context, opts catalog.ListOptions) ([]catalog.Model, error) {
	models, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	out := make([]catalog.Model, 0, len(models))
	for _, m := range models {
		if query != "" && !strings.Contains(strings.ToLower(m.ModelName), query) {
			continue
		}
		out = append(out, toModel(m))
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

// Detail implements catalog.Source.
//
// Detail resolves against the *unfiltered* index, unlike List. A model can be
// retired between the moment it was pinned in config and the moment its
// detail is asked for, and answering "unknown model" then would be actively
// misleading — the useful answer is the retirement and its ReplacedBy
// successor, which is exactly what a stale pin needs to report.
func (s *Source) Detail(ctx context.Context, id string) (catalog.Detail, error) {
	models, err := s.indexAll(ctx)
	if err != nil {
		return catalog.Detail{}, err
	}
	for _, m := range models {
		if m.ModelName == id {
			return toDetail(m), nil
		}
	}
	return catalog.Detail{}, fmt.Errorf("deepinfracatalog: unknown model %q", id)
}

// fetch retrieves and decodes the raw index, bounding the request with its own
// timeout so a stalled endpoint fails fast rather than holding the caller.
func (s *Source) fetch(ctx context.Context) ([]wireModel, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.indexURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepinfracatalog: fetch index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepinfracatalog: index returned %s", resp.Status)
	}
	var models []wireModel
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("deepinfracatalog: decode index: %w", err)
	}
	return models, nil
}
