package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AgentBinaryInfo holds metadata about an uploaded agent binary.
type AgentBinaryInfo struct {
	Version    string `json:"version"`
	Checksum   string `json:"checksum"`   // SHA-256 hex
	Size       int64  `json:"size"`
	OS         string `json:"os"`         // e.g. "linux"
	Arch       string `json:"arch"`       // e.g. "amd64"
	UploadedAt int64  `json:"uploaded_at"`
	FilePath   string `json:"-"` // internal, not serialized to API
}

// UpdateStore manages uploaded agent binaries.
type UpdateStore struct {
	mu       sync.RWMutex
	dataDir  string
	binaries map[string]*AgentBinaryInfo // key: "os/arch"
}

// NewUpdateStore creates a new update store.
func NewUpdateStore(dataDir string) *UpdateStore {
	s := &UpdateStore{
		dataDir:  filepath.Join(dataDir, "agent-binaries"),
		binaries: make(map[string]*AgentBinaryInfo),
	}
	s.loadIndex()
	return s
}

// Store saves an agent binary to disk and records its metadata.
func (s *UpdateStore) Store(version, osName, arch string, data []byte) (*AgentBinaryInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create binary dir: %w", err)
	}

	checksum := sha256Hex(data)
	filename := fmt.Sprintf("agent-%s-%s-%s", version, osName, arch)
	filePath := filepath.Join(s.dataDir, filename)

	if err := os.WriteFile(filePath, data, 0755); err != nil {
		return nil, fmt.Errorf("write binary: %w", err)
	}

	info := &AgentBinaryInfo{
		Version:    version,
		Checksum:   checksum,
		Size:       int64(len(data)),
		OS:         osName,
		Arch:       arch,
		UploadedAt: time.Now().Unix(),
		FilePath:   filePath,
	}

	key := osName + "/" + arch
	s.binaries[key] = info
	s.saveIndex()

	log.Printf("stored agent binary: %s (%s/%s, %d bytes, sha256:%s)", version, osName, arch, len(data), checksum[:12])
	return info, nil
}

// Get returns the binary info for a specific OS/arch combo.
func (s *UpdateStore) Get(osName, arch string) *AgentBinaryInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.binaries[osName+"/"+arch]
}

// GetBinary returns the binary data for a specific OS/arch.
func (s *UpdateStore) GetBinary(osName, arch string) ([]byte, *AgentBinaryInfo, error) {
	s.mu.RLock()
	info := s.binaries[osName+"/"+arch]
	s.mu.RUnlock()

	if info == nil {
		return nil, nil, fmt.Errorf("no binary for %s/%s", osName, arch)
	}

	data, err := os.ReadFile(info.FilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read binary: %w", err)
	}

	return data, info, nil
}

// List returns all stored binary infos.
func (s *UpdateStore) List() []*AgentBinaryInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*AgentBinaryInfo
	for _, info := range s.binaries {
		result = append(result, info)
	}
	return result
}

// LatestVersion returns the version string of the most recently uploaded binary.
func (s *UpdateStore) LatestVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var latest *AgentBinaryInfo
	for _, info := range s.binaries {
		if latest == nil || info.UploadedAt > latest.UploadedAt {
			latest = info
		}
	}
	if latest != nil {
		return latest.Version
	}
	return ""
}

func (s *UpdateStore) loadIndex() {
	indexPath := filepath.Join(s.dataDir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return
	}

	var entries map[string]*AgentBinaryInfo
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}

	// Restore file paths
	for key, info := range entries {
		filename := fmt.Sprintf("agent-%s-%s-%s", info.Version, info.OS, info.Arch)
		info.FilePath = filepath.Join(s.dataDir, filename)
		s.binaries[key] = info
	}
}

func (s *UpdateStore) saveIndex() {
	indexPath := filepath.Join(s.dataDir, "index.json")
	data, err := json.MarshalIndent(s.binaries, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(indexPath, data, 0644)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
