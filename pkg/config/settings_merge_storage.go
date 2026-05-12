package config

// This file contains merge/clone helpers for the larger sub-blocks at the
// bottom of MergeSettings: Failover, WebAuth, Aigo, and the StorageConfig
// tree (OSFS, Embedded, S3).

func mergeFailover(lower, higher *FailoverConfig) *FailoverConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneFailover(higher)
	}
	if higher == nil {
		return cloneFailover(lower)
	}
	out := cloneFailover(lower)
	if higher.Enabled != nil {
		out.Enabled = boolPtr(*higher.Enabled)
	}
	if len(higher.Models) > 0 {
		out.Models = make([]FailoverModelEntry, len(higher.Models))
		copy(out.Models, higher.Models)
	}
	if higher.MaxRetries != 0 {
		out.MaxRetries = higher.MaxRetries
	}
	if len(higher.PrimaryKeyPool) > 0 {
		out.PrimaryKeyPool = cloneProviderKeys(higher.PrimaryKeyPool)
	}
	return out
}

func cloneFailover(src *FailoverConfig) *FailoverConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.Enabled = cloneBoolPtr(src.Enabled)
	if len(src.Models) > 0 {
		out.Models = make([]FailoverModelEntry, len(src.Models))
		copy(out.Models, src.Models)
	}
	if len(src.PrimaryKeyPool) > 0 {
		out.PrimaryKeyPool = cloneProviderKeys(src.PrimaryKeyPool)
	}
	return &out
}

func cloneProviderKeys(src []ProviderKey) []ProviderKey {
	if len(src) == 0 {
		return nil
	}
	out := make([]ProviderKey, len(src))
	for i, k := range src {
		cp := k
		if len(k.Models) > 0 {
			cp.Models = append([]string(nil), k.Models...)
		}
		out[i] = cp
	}
	return out
}

// mergeBifrost performs a wholesale replacement when higher is non-nil for
// either sub-block. Sub-block fields are simple value structs; deep-merging
// inner maps would surprise the UI which always sends a complete patch.
func mergeBifrost(lower, higher *BifrostConfig) *BifrostConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneBifrost(higher)
	}
	if higher == nil {
		return cloneBifrost(lower)
	}
	out := cloneBifrost(lower)
	if higher.SemanticCache != nil {
		out.SemanticCache = cloneSemanticCache(higher.SemanticCache)
	}
	if higher.Telemetry != nil {
		out.Telemetry = cloneTelemetry(higher.Telemetry)
	}
	return out
}

func cloneBifrost(src *BifrostConfig) *BifrostConfig {
	if src == nil {
		return nil
	}
	return &BifrostConfig{
		SemanticCache: cloneSemanticCache(src.SemanticCache),
		Telemetry:     cloneTelemetry(src.Telemetry),
	}
}

func cloneSemanticCache(src *SemanticCacheConfig) *SemanticCacheConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.Enabled = cloneBoolPtr(src.Enabled)
	out.CacheByModel = cloneBoolPtr(src.CacheByModel)
	out.CacheByProvider = cloneBoolPtr(src.CacheByProvider)
	out.ExcludeSystemPrompt = cloneBoolPtr(src.ExcludeSystemPrompt)
	out.VectorStore = cloneVectorStore(src.VectorStore)
	return &out
}

func cloneVectorStore(src *VectorStoreConfig) *VectorStoreConfig {
	if src == nil {
		return nil
	}
	out := *src
	if len(src.Headers) > 0 {
		out.Headers = make(map[string]string, len(src.Headers))
		for k, v := range src.Headers {
			out.Headers[k] = v
		}
	}
	return &out
}

func cloneTelemetry(src *TelemetryConfig) *TelemetryConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.Enabled = cloneBoolPtr(src.Enabled)
	out.Insecure = cloneBoolPtr(src.Insecure)
	out.MetricsEnabled = cloneBoolPtr(src.MetricsEnabled)
	if len(src.Headers) > 0 {
		out.Headers = make(map[string]string, len(src.Headers))
		for k, v := range src.Headers {
			out.Headers[k] = v
		}
	}
	return &out
}

func mergeGovernance(lower, higher *GovernanceConfig) *GovernanceConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneGovernance(higher)
	}
	if higher == nil {
		return cloneGovernance(lower)
	}
	out := cloneGovernance(lower)
	if higher.Enabled != nil {
		out.Enabled = boolPtr(*higher.Enabled)
	}
	// Higher's VirtualKeys, when non-nil, fully replaces lower's slice. The UI
	// always patches the entire list, so per-element merging would conflict
	// with the user's intent (e.g. deleting a key would silently fail).
	if higher.VirtualKeys != nil {
		out.VirtualKeys = cloneVirtualKeys(higher.VirtualKeys)
	}
	return out
}

func cloneGovernance(src *GovernanceConfig) *GovernanceConfig {
	if src == nil {
		return nil
	}
	return &GovernanceConfig{
		Enabled:     cloneBoolPtr(src.Enabled),
		VirtualKeys: cloneVirtualKeys(src.VirtualKeys),
	}
}

func cloneVirtualKeys(src []GovernanceVirtualKey) []GovernanceVirtualKey {
	if len(src) == 0 {
		return nil
	}
	out := make([]GovernanceVirtualKey, len(src))
	for i, k := range src {
		cp := k
		if len(k.AllowedModels) > 0 {
			cp.AllowedModels = append([]string(nil), k.AllowedModels...)
		}
		out[i] = cp
	}
	return out
}

func cloneWebAuth(src *WebAuthConfig) *WebAuthConfig {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

// mergeStorage merges StorageConfig with sub-blocks. Higher non-zero scalars
// win; sub-blocks (OSFS / Embedded / S3) are themselves field-merged so a
// partial higher patch (e.g. only embedded.bucket) doesn't wipe lower fields.
func mergeStorage(lower, higher *StorageConfig) *StorageConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneStorageConfig(higher)
	}
	if higher == nil {
		return cloneStorageConfig(lower)
	}
	out := cloneStorageConfig(lower)
	if higher.Backend != "" {
		out.Backend = higher.Backend
	}
	if higher.PublicBaseURL != "" {
		out.PublicBaseURL = higher.PublicBaseURL
	}
	if higher.TenantPrefix != "" {
		out.TenantPrefix = higher.TenantPrefix
	}
	out.OSFS = mergeStorageOSFS(lower.OSFS, higher.OSFS)
	out.Embedded = mergeStorageEmbedded(lower.Embedded, higher.Embedded)
	out.S3 = mergeStorageS3(lower.S3, higher.S3)
	return out
}

func mergeStorageOSFS(lower, higher *StorageOSFSConfig) *StorageOSFSConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneStorageOSFS(higher)
	}
	if higher == nil {
		return cloneStorageOSFS(lower)
	}
	out := cloneStorageOSFS(lower)
	if higher.Root != "" {
		out.Root = higher.Root
	}
	return out
}

func mergeStorageEmbedded(lower, higher *StorageEmbeddedConfig) *StorageEmbeddedConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneStorageEmbedded(higher)
	}
	if higher == nil {
		return cloneStorageEmbedded(lower)
	}
	out := cloneStorageEmbedded(lower)
	if higher.Mode != "" {
		out.Mode = higher.Mode
	}
	if higher.Addr != "" {
		out.Addr = higher.Addr
	}
	if higher.Root != "" {
		out.Root = higher.Root
	}
	if higher.Bucket != "" {
		out.Bucket = higher.Bucket
	}
	if higher.AccessKey != "" {
		out.AccessKey = higher.AccessKey
	}
	if higher.SecretKey != "" {
		out.SecretKey = higher.SecretKey
	}
	return out
}

func mergeStorageS3(lower, higher *StorageS3Config) *StorageS3Config {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneStorageS3(higher)
	}
	if higher == nil {
		return cloneStorageS3(lower)
	}
	out := cloneStorageS3(lower)
	if higher.Endpoint != "" {
		out.Endpoint = higher.Endpoint
	}
	if higher.Region != "" {
		out.Region = higher.Region
	}
	if higher.Bucket != "" {
		out.Bucket = higher.Bucket
	}
	if higher.AccessKeyID != "" {
		out.AccessKeyID = higher.AccessKeyID
	}
	if higher.SecretAccessKey != "" {
		out.SecretAccessKey = higher.SecretAccessKey
	}
	if higher.UsePathStyle {
		out.UsePathStyle = true
	}
	if higher.PublicBaseURL != "" {
		out.PublicBaseURL = higher.PublicBaseURL
	}
	return out
}

func cloneStorageConfig(src *StorageConfig) *StorageConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.OSFS = cloneStorageOSFS(src.OSFS)
	out.Embedded = cloneStorageEmbedded(src.Embedded)
	out.S3 = cloneStorageS3(src.S3)
	return &out
}

func cloneStorageOSFS(src *StorageOSFSConfig) *StorageOSFSConfig {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

func cloneStorageEmbedded(src *StorageEmbeddedConfig) *StorageEmbeddedConfig {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

func cloneStorageS3(src *StorageS3Config) *StorageS3Config {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

func cloneAigoConfig(src *AigoConfig) *AigoConfig {
	if src == nil {
		return nil
	}
	out := AigoConfig{Timeout: src.Timeout}
	if len(src.Providers) > 0 {
		out.Providers = make(map[string]AigoProvider, len(src.Providers))
		for k, p := range src.Providers {
			cp := p
			if len(p.Metadata) > 0 {
				cp.Metadata = make(map[string]string, len(p.Metadata))
				for mk, mv := range p.Metadata {
					cp.Metadata[mk] = mv
				}
			}
			out.Providers[k] = cp
		}
	}
	if len(src.Routing) > 0 {
		out.Routing = make(map[string][]string, len(src.Routing))
		for k, v := range src.Routing {
			out.Routing[k] = append([]string(nil), v...)
		}
	}
	return &out
}
