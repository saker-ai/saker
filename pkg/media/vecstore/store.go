// Package vecstore provides vector storage and similarity search
// using chromem-go as the embedded vector database.
package vecstore

// Hit represents a single search result from the vector store.
type Hit struct {
	ID       string            `json:"id"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// StoreStats reports the state of the vector store.
type StoreStats struct {
	DocumentCount int    `json:"document_count"`
	Backend       string `json:"backend"`
}
