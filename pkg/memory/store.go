// Package memory provides file-based session memory persistence.
// Memory entries are stored as individual markdown files with YAML frontmatter
// in a directory (e.g., .saker/memory/), with MEMORY.md as an index.
package memory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryType defines the category of a memory entry.
type MemoryType string

const (
	MemoryTypeUser      MemoryType = "user"
	MemoryTypeFeedback  MemoryType = "feedback"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeReference MemoryType = "reference"
)

// ValidMemoryTypes lists all valid memory types.
var ValidMemoryTypes = []MemoryType{
	MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference,
}

// IsValidType checks if a MemoryType is one of the known types.
func IsValidType(t MemoryType) bool {
	for _, valid := range ValidMemoryTypes {
		if t == valid {
			return true
		}
	}
	return false
}

const (
	indexFile          = "MEMORY.md"
	maxEntrypointLines = 200
	maxEntrypointBytes = 25000
	maxIndexLineLen    = 150
)

// Entry represents a single memory file with frontmatter.
type Entry struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        MemoryType `json:"type"`
	Content     string     `json:"content"`  // body after frontmatter
	FilePath    string     `json:"filepath"` // absolute path
	ModTime     time.Time  `json:"mod_time"`
}

// Store manages file-based memory in a directory.
type Store struct {
	dir string
	mu  sync.RWMutex
}

// NewStore creates a Store rooted at the given directory.
// The directory is created if it does not exist.
func NewStore(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("memory: create dir: %w", err)
	}
	return &Store{dir: abs}, nil
}

// Dir returns the absolute path of the memory directory.
func (s *Store) Dir() string { return s.dir }

// Save writes a memory entry to a file and updates the index.
func (s *Store) Save(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("memory: entry name is required")
	}
	if !IsValidType(entry.Type) {
		return fmt.Errorf("memory: invalid type %q", entry.Type)
	}

	filename := sanitizeFilename(entry.Name) + ".md"
	path := filepath.Join(s.dir, filename)

	content := buildFileContent(entry)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("memory: write %s: %w", filename, err)
	}

	return s.updateIndexLocked()
}

// Load reads a single memory entry by name.
func (s *Store) Load(name string) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filename := sanitizeFilename(name) + ".md"
	path := filepath.Join(s.dir, filename)
	return parseEntryFile(path)
}

// List returns all memory entries sorted by type then name.
func (s *Store) List() ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listEntriesLocked()
}

// Delete removes a memory entry and updates the index.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	filename := sanitizeFilename(name) + ".md"
	path := filepath.Join(s.dir, filename)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // idempotent
		}
		return fmt.Errorf("memory: delete %s: %w", filename, err)
	}
	return s.updateIndexLocked()
}

// LoadIndex reads and returns the MEMORY.md content.
func (s *Store) LoadIndex() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, indexFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("memory: read index: %w", err)
	}
	return truncateIndex(string(data)), nil
}

// UpdateIndex rebuilds MEMORY.md from all entry files.
func (s *Store) UpdateIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateIndexLocked()
}

func (s *Store) listEntriesLocked() ([]Entry, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("memory: list dir: %w", err)
	}
	var result []Entry
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") || de.Name() == indexFile {
			continue
		}
		path := filepath.Join(s.dir, de.Name())
		entry, err := parseEntryFile(path)
		if err != nil {
			continue // skip unparseable files
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *Store) updateIndexLocked() error {
	entries, err := s.listEntriesLocked()
	if err != nil {
		return err
	}
	var sb strings.Builder
	for _, entry := range entries {
		filename := sanitizeFilename(entry.Name) + ".md"
		desc := entry.Description
		if desc == "" {
			desc = string(entry.Type)
		}
		line := fmt.Sprintf("- [%s](%s) — %s", entry.Name, filename, desc)
		if len(line) > maxIndexLineLen {
			line = line[:maxIndexLineLen-1] + "…"
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	path := filepath.Join(s.dir, indexFile)
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// buildFileContent creates the markdown file with YAML frontmatter.
func buildFileContent(entry Entry) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", entry.Name))
	sb.WriteString(fmt.Sprintf("description: %s\n", entry.Description))
	sb.WriteString(fmt.Sprintf("type: %s\n", entry.Type))
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(entry.Content))
	sb.WriteString("\n")
	return sb.String()
}

// parseEntryFile reads a markdown file with YAML frontmatter.
func parseEntryFile(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Entry{}, err
	}

	content := string(data)
	entry := Entry{
		FilePath: path,
		ModTime:  info.ModTime(),
	}

	// Parse YAML frontmatter delimited by "---".
	if !strings.HasPrefix(content, "---\n") {
		entry.Content = content
		entry.Name = filenameToName(filepath.Base(path))
		return entry, nil
	}
	rest := content[4:] // skip opening "---\n"
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		entry.Content = content
		entry.Name = filenameToName(filepath.Base(path))
		return entry, nil
	}

	frontmatter := rest[:endIdx]
	body := rest[endIdx+5:] // skip "\n---\n"

	entry.Content = strings.TrimSpace(body)
	entry.Name = filenameToName(filepath.Base(path)) // default

	// Parse simple YAML key: value pairs.
	scanner := bufio.NewScanner(strings.NewReader(frontmatter))
	for scanner.Scan() {
		line := scanner.Text()
		key, val, ok := parseYAMLLine(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			entry.Name = val
		case "description":
			entry.Description = val
		case "type":
			entry.Type = MemoryType(val)
		}
	}
	return entry, nil
}

func parseYAMLLine(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	return key, value, true
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	// Replace spaces and special chars with underscores.
	replacer := strings.NewReplacer(
		" ", "_", "/", "_", "\\", "_", ":", "_",
		"*", "_", "?", "_", "\"", "_", "<", "_",
		">", "_", "|", "_",
	)
	name = replacer.Replace(name)
	name = strings.ToLower(name)
	if name == "" {
		name = "unnamed"
	}
	return name
}

func filenameToName(filename string) string {
	name := strings.TrimSuffix(filename, ".md")
	return strings.ReplaceAll(name, "_", " ")
}

// truncateIndex enforces maxEntrypointLines and maxEntrypointBytes.
func truncateIndex(content string) string {
	if len(content) > maxEntrypointBytes {
		content = content[:maxEntrypointBytes]
	}
	lines := strings.SplitN(content, "\n", maxEntrypointLines+1)
	if len(lines) > maxEntrypointLines {
		lines = lines[:maxEntrypointLines]
	}
	return strings.Join(lines, "\n")
}
