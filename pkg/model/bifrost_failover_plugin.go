// bifrost_failover_plugin.go: an LLMPlugin that observes Bifrost's SDK-level
// Fallbacks routing and emits a saker-style OnFailover callback when the
// resolved provider/model differs from the primary. Detection is done in
// PostLLMHook by comparing BifrostResponseExtraFields.Provider /
// ResolvedModelUsed against the primary recorded at registration time.
package model

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// failoverObserverPlugin is a thin LLMPlugin that doesn't modify requests or
// responses — it only observes the resolved provider/model on each completed
// call and invokes OnFailover when Bifrost's Fallbacks routing dispatched to
// something other than the primary.
type failoverObserverPlugin struct {
	primaryProvider schemas.ModelProvider
	primaryModel    string
	onFailover      func(from, to string, statusCode int, message string)
}

func newFailoverObserverPlugin(primaryProvider schemas.ModelProvider, primaryModel string, cb func(from, to string, statusCode int, message string)) *failoverObserverPlugin {
	return &failoverObserverPlugin{
		primaryProvider: primaryProvider,
		primaryModel:    primaryModel,
		onFailover:      cb,
	}
}

func (p *failoverObserverPlugin) GetName() string { return "saker.failover_observer" }
func (p *failoverObserverPlugin) Cleanup() error  { return nil }

// PreLLMHook is a no-op: we only observe outcomes, never mutate inputs.
func (p *failoverObserverPlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}

// PostLLMHook fires after every Bifrost call (success OR error). Compare the
// resolved provider/model in ExtraFields against the primary; if different,
// Bifrost's Fallbacks routing kicked in and we surface that to saker callers.
func (p *failoverObserverPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if p.onFailover == nil {
		return resp, bifrostErr, nil
	}

	provider, resolvedModel, statusCode, message := extractFailoverObservation(resp, bifrostErr)

	if provider == "" {
		return resp, bifrostErr, nil
	}

	// Detect failover: provider differs OR (same provider but resolved model
	// differs from primary). Same primary provider+model means the primary
	// answered — no callback.
	primaryName := buildModelLabel(p.primaryProvider, p.primaryModel)
	resolvedName := buildModelLabel(provider, resolvedModel)
	if provider == p.primaryProvider && (resolvedModel == "" || resolvedModel == p.primaryModel) {
		return resp, bifrostErr, nil
	}

	p.onFailover(primaryName, resolvedName, statusCode, message)
	return resp, bifrostErr, nil
}

// extractFailoverObservation pulls (provider, resolvedModel, status, message)
// from whichever side of the BifrostResponse / BifrostError union is non-nil.
// All fields are zero-valued when neither side carries the data.
func extractFailoverObservation(resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (schemas.ModelProvider, string, int, string) {
	if resp != nil && resp.ChatResponse != nil {
		ef := resp.ChatResponse.ExtraFields
		return ef.Provider, ef.ResolvedModelUsed, 0, ""
	}
	if bifrostErr != nil {
		ef := bifrostErr.ExtraFields
		status := 0
		if bifrostErr.StatusCode != nil {
			status = *bifrostErr.StatusCode
		}
		message := ""
		if bifrostErr.Error != nil {
			message = bifrostErr.Error.Message
		}
		return ef.Provider, ef.ResolvedModelUsed, status, message
	}
	return "", "", 0, ""
}

// buildModelLabel formats a "provider/model" string for the OnFailover
// callback. Empty model collapses to just the provider.
func buildModelLabel(provider schemas.ModelProvider, model string) string {
	p := string(provider)
	m := strings.TrimSpace(model)
	if m == "" {
		return p
	}
	return p + "/" + m
}
