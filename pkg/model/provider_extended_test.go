package model

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests construct providers but don't make real network calls. Bifrost
// engine instantiation is in-memory; provider auth flows fire on first request.

func TestOllamaProvider_DefaultsToLocalhost(t *testing.T) {
	p := &OllamaProvider{ModelName: "llama3"}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err)
	require.NotNil(t, mdl)
	assert.Equal(t, "llama3", mdl.(*bifrostModel).modelName)
}

func TestOllamaProvider_RespectsBaseURL(t *testing.T) {
	p := &OllamaProvider{
		BaseURL:   "http://other-host:11434",
		ModelName: "qwen2",
	}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err)
	bm := mdl.(*bifrostModel)
	assert.Equal(t, "http://other-host:11434", bm.srcConfig.BaseURL)
}

func TestOllamaProvider_CachesWhenTTLSet(t *testing.T) {
	p := &OllamaProvider{
		ModelName: "llama3",
		CacheTTL:  time.Hour,
	}
	a, err := p.Model(context.Background())
	require.NoError(t, err)
	b, err := p.Model(context.Background())
	require.NoError(t, err)
	assert.Same(t, a, b, "cached Model must be reused while TTL valid")
}

func TestBedrockProvider_RequiresRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	p := &BedrockProvider{
		AccessKey: "AKIAFAKE",
		SecretKey: "secret",
		ModelName: "anthropic.claude-sonnet-4-20250514",
	}
	_, err := p.Model(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region")
}

func TestBedrockProvider_PicksUpAWSRegionEnv(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	p := &BedrockProvider{
		AccessKey: "AKIAFAKE",
		SecretKey: "secret",
		ModelName: "anthropic.claude-sonnet-4-20250514",
	}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err)
	require.NotNil(t, mdl)
}

func TestBedrockProvider_AllowsIAMRole(t *testing.T) {
	// Empty access/secret + region set → IAM-role mode is valid.
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	p := &BedrockProvider{
		Region:    "us-west-2",
		ModelName: "anthropic.claude-sonnet-4-20250514",
	}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err, "IAM-role auth should not require explicit keys")
	require.NotNil(t, mdl)
}

func TestVertexProvider_RequiresProjectID(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	p := &VertexProvider{
		Region:          "us-central1",
		AuthCredentials: `{"type":"service_account"}`,
		ModelName:       "gemini-1.5-pro",
	}
	_, err := p.Model(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project_id")
}

func TestVertexProvider_DefaultsRegion(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_REGION", "")
	p := &VertexProvider{
		ProjectID:       "my-project",
		AuthCredentials: `{"type":"service_account"}`,
		ModelName:       "gemini-1.5-pro",
	}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err)
	require.NotNil(t, mdl)
}

func TestAzureProvider_RequiresEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	p := &AzureProvider{
		APIKey:    "fake-key",
		ModelName: "gpt-4o",
	}
	_, err := p.Model(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestAzureProvider_RequiresAuth(t *testing.T) {
	p := &AzureProvider{
		Endpoint:  "https://my-resource.openai.azure.com",
		ModelName: "gpt-4o",
		// No APIKey, no ClientID — invalid.
	}
	_, err := p.Model(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api key or client_id")
}

func TestAzureProvider_AcceptsAPIKey(t *testing.T) {
	p := &AzureProvider{
		Endpoint:  "https://my-resource.openai.azure.com",
		APIKey:    "fake-azure-key",
		ModelName: "gpt-4o-deployment",
	}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err)
	require.NotNil(t, mdl)
}

func TestAzureProvider_AcceptsClientSecret(t *testing.T) {
	p := &AzureProvider{
		Endpoint:     "https://my-resource.openai.azure.com",
		ClientID:     "client-id-uuid",
		ClientSecret: "secret",
		TenantID:     "tenant-uuid",
		ModelName:    "gpt-4o-deployment",
	}
	mdl, err := p.Model(context.Background())
	require.NoError(t, err)
	require.NotNil(t, mdl)
}

func TestNewBifrost_AcceptsTypedAuthWithoutAPIKey(t *testing.T) {
	// Verifies the auth-requirement relaxation: if any typed key config is set,
	// APIKey is no longer mandatory.
	mdl, err := NewBifrost(BifrostConfig{
		Provider:        BifrostProviderOllama,
		ModelName:       "llama3",
		OllamaKeyConfig: nil, // even nil typed config should still require key
	})
	require.Error(t, err, "no typed auth and no APIKey should fail")
	assert.Nil(t, mdl)
}
