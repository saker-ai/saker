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
	return &out
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
