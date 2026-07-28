package providers

import (
	"context"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

// FetchModels queries a provider's /models endpoint for the model ids it offers,
// the same way the dashboard does. This surfaces models a custom or dynamic
// provider exposes even when they are not listed statically in the config.
func FetchModels(ctx context.Context, cfg *config.Config, providerID string) ([]string, error) {
	id, p := cfg.ResolveProvider(providerID)
	client, err := llm.New(llm.Options{
		Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey, Headers: p.Headers,
		ProviderID: id, Timeout: 20 * time.Second, APIVersion: p.APIVersion, Region: p.Region,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	infos, err := client.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(infos))
	for _, mi := range infos {
		if mi.ID != "" {
			out = append(out, mi.ID)
		}
	}
	return out, nil
}
