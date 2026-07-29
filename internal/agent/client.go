package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/enowdev/antares/internal/llm"
)

// newClient builds the provider adapter for a model. When the model name is
// qualified as "provider/model", the prefix selects the provider. When fallback
// models are configured, the returned client tries each in turn on a hard
// failure.
func (a *Agent) newClient(modelOverride string) (client llm.Client, model, provider string, err error) {
	primary, model, provider, err := a.resolveClient(modelOverride)
	if err != nil {
		return nil, "", "", err
	}

	// Only the default path (no explicit override) uses the fallback chain, so
	// a deliberately chosen model is honoured exactly.
	entries := []llm.FallbackEntry{{Client: primary, Model: model}}
	if modelOverride == "" {
		for _, spec := range a.cfg.Model.Fallback {
			spec = strings.TrimSpace(spec)
			if spec == "" {
				continue
			}
			fc, fm, _, ferr := a.resolveClient(spec)
			if ferr != nil {
				slog.Debug("fallback model unavailable", "spec", spec, "error", ferr)
				continue
			}
			entries = append(entries, llm.FallbackEntry{Client: fc, Model: fm})
		}
	}
	return llm.NewFallback(entries), model, provider, nil
}

// resolveClient builds one provider adapter for a model spec.
func (a *Agent) resolveClient(modelOverride string) (client llm.Client, model, provider string, err error) {
	cfg := a.cfg
	model = firstNonEmpty(modelOverride, cfg.Model.Default)
	provider = cfg.Model.Provider
	if modelOverride != "" {
		provider = "" // a spec resolves its own provider from a prefix
	}

	if model == "" {
		return nil, "", "", fmt.Errorf("no model selected — set model.default in Settings or pick one on the Models page")
	}

	// A "provider/model" prefix selects the provider only when none is
	// configured. Aggregators like OpenRouter use slash-qualified model ids
	// ("anthropic/claude-…") that must be passed through untouched.
	if provider == "" {
		if name, rest, ok := strings.Cut(model, "/"); ok && rest != "" {
			if _, exists := cfg.Providers[name]; exists {
				provider, model = name, rest
			}
		}
	}

	id, p := cfg.ResolveProvider(provider)
	if !p.Enabled && p.BaseURL == "" {
		return nil, "", "", fmt.Errorf("provider %q is disabled and has no base_url", id)
	}
	timeout := time.Duration(p.TimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	retries := cfg.Model.MaxRetries
	switch {
	case retries == 0:
		retries = 3 // unset → a sensible default
	case retries < 0:
		retries = 0 // explicit opt-out
	}
	client, err = llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Headers: p.Headers, Timeout: timeout, ProviderID: id,
		Retries:    retries,
		APIVersion: p.APIVersion, Region: p.Region,
	})
	if err != nil {
		return nil, "", "", err
	}
	return client, model, id, nil
}

// newAuxClient returns the auxiliary model used for summarisation and other
// background work, falling back to the main model.
func (a *Agent) newAuxClient() (llm.Client, string, string, error) {
	if aux := strings.TrimSpace(a.cfg.Model.Auxiliary); aux != "" {
		return a.newClient(aux)
	}
	return a.newClient("")
}

// Probe checks whether the configured provider answers, for /api/status.
func (a *Agent) Probe(ctx context.Context) (bool, string) {
	client, model, provider, err := a.newClient("")
	if err != nil {
		return false, err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	_, err = client.Chat(ctx, llm.Request{
		Model:     model,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "ping"}},
		MaxTokens: 8,
	})
	if err != nil {
		switch {
		case llm.IsAuthError(err):
			return false, fmt.Sprintf("the API key for %s was rejected", provider)
		case llm.IsRateLimit(err):
			return true, "provider is rate limiting, but the credentials are valid"
		default:
			return false, err.Error()
		}
	}
	return true, fmt.Sprintf("%s · %s ready", provider, model)
}

// Models lists the models a provider offers: whatever its /models endpoint
// reports, merged with any models added by hand in config (providers.<id>.
// models). Manually added ids that the endpoint does not return are appended,
// carrying a context window from providers.<id>.model_meta when set. A live
// fetch that fails still yields the manual list rather than nothing.
func (a *Agent) Models(ctx context.Context, providerID string) ([]llm.ModelInfo, error) {
	id, p := a.cfg.ResolveProvider(providerID)
	client, err := llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey,
		Headers: p.Headers, ProviderID: id, Timeout: 60 * time.Second, APIVersion: p.APIVersion, Region: p.Region,
	})
	if err != nil {
		return nil, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	live, ferr := client.Models(fetchCtx)

	// Fold in manually configured models the endpoint did not return.
	seen := make(map[string]bool, len(live))
	for _, m := range live {
		seen[m.ID] = true
	}
	out := live
	for _, mid := range p.Models {
		if mid == "" || seen[mid] {
			continue
		}
		seen[mid] = true
		out = append(out, llm.ModelInfo{
			ID:            mid,
			Name:          mid,
			Provider:      id,
			ContextWindow: p.ModelMeta[mid].ContextWindow,
		})
	}

	// A failed fetch is only an error when it left us with nothing to show.
	if ferr != nil && len(out) == 0 {
		return nil, ferr
	}
	return out, nil
}
