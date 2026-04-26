// Package filestore manages user-uploaded config and scenario files.
// Files are written to the local filesystem and persisted to Kubernetes
// ConfigMaps so they survive pod restarts.
package filestore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CategoryConfigs   = "configs"
	CategoryScenarios = "scenarios"

	cmConfigs   = "auto-run-user-configs"
	cmScenarios = "auto-run-user-scenarios"
	namespace   = "default"
)

// FileInfo describes one uploaded file.
type FileInfo struct {
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Size      int       `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store manages uploaded files on the local filesystem and in ConfigMaps.
type Store struct {
	baseDir string // e.g. /uploads
	k8s     client.Client
}

// New creates a Store rooted at baseDir.
// baseDir/<category>/<filename> is the layout on disk.
func New(baseDir string, k8s client.Client) *Store {
	return &Store{baseDir: baseDir, k8s: k8s}
}

// Init creates the required subdirectories.
func (s *Store) Init() error {
	for _, cat := range []string{CategoryConfigs, CategoryScenarios} {
		if err := os.MkdirAll(s.dir(cat), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Save writes content to disk and syncs to the ConfigMap.
func (s *Store) Save(ctx context.Context, category, name string, content []byte) error {
	if err := validateCategory(category); err != nil {
		return err
	}
	if err := validateName(name); err != nil {
		return err
	}
	if err := os.WriteFile(s.path(category, name), content, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return s.syncToConfigMap(ctx, category)
}

// Load returns the content of a file.
func (s *Store) Load(category, name string) ([]byte, error) {
	if err := validateCategory(category); err != nil {
		return nil, err
	}
	return os.ReadFile(s.path(category, name))
}

// Delete removes a file and syncs the ConfigMap.
func (s *Store) Delete(ctx context.Context, category, name string) error {
	if err := validateCategory(category); err != nil {
		return err
	}
	if err := os.Remove(s.path(category, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.syncToConfigMap(ctx, category)
}

// List returns all files in a category, sorted by name.
func (s *Store) List(category string) ([]FileInfo, error) {
	if err := validateCategory(category); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir(category))
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, err
	}
	var files []FileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, _ := e.Info()
		size := 0
		updatedAt := time.Time{}
		if info != nil {
			size = int(info.Size())
			updatedAt = info.ModTime()
		}
		files = append(files, FileInfo{
			Name:      e.Name(),
			Category:  category,
			Size:      size,
			UpdatedAt: updatedAt,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// Resolve returns the absolute path to use for a given run field value.
// If the file exists in the uploads directory, it takes priority over repoRoot.
// This allows uploaded files to override repo-committed configs/scenarios.
func (s *Store) Resolve(category, fieldValue, repoRoot string) string {
	name := filepath.Base(fieldValue)
	uploadPath := s.path(category, name)
	if _, err := os.Stat(uploadPath); err == nil {
		return uploadPath
	}
	return filepath.Join(repoRoot, fieldValue)
}

// RestoreFromConfigMaps reads both ConfigMaps and writes files back to disk.
// Call at server startup to restore files after a pod restart.
func (s *Store) RestoreFromConfigMaps(ctx context.Context) error {
	if err := s.restoreCategory(ctx, CategoryConfigs, cmConfigs); err != nil {
		return err
	}
	return s.restoreCategory(ctx, CategoryScenarios, cmScenarios)
}

// ── private ──────────────────────────────────────────────────────────────────

func (s *Store) dir(category string) string {
	return filepath.Join(s.baseDir, category)
}

func (s *Store) path(category, name string) string {
	return filepath.Join(s.dir(category), name)
}

func (s *Store) cmName(category string) string {
	if category == CategoryConfigs {
		return cmConfigs
	}
	return cmScenarios
}

func (s *Store) syncToConfigMap(ctx context.Context, category string) error {
	entries, err := os.ReadDir(s.dir(category))
	if err != nil {
		return err
	}
	data := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		content, err := os.ReadFile(s.path(category, e.Name()))
		if err != nil {
			continue
		}
		// ConfigMap keys cannot contain slashes; dots are fine.
		data[e.Name()] = string(content)
	}
	return s.applyConfigMap(ctx, s.cmName(category), data)
}

func (s *Store) restoreCategory(ctx context.Context, category, cmName string) error {
	var cm corev1.ConfigMap
	key := client.ObjectKey{Namespace: namespace, Name: cmName}
	if err := s.k8s.Get(ctx, key, &cm); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(s.dir(category), 0o755); err != nil {
		return err
	}
	for name, content := range cm.Data {
		if err := os.WriteFile(s.path(category, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("restore %s/%s: %w", category, name, err)
		}
	}
	return nil
}

func (s *Store) applyConfigMap(ctx context.Context, name string, data map[string]string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"managed-by": "auto-run"},
		},
		Data: data,
	}
	var existing corev1.ConfigMap
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := s.k8s.Get(ctx, key, &existing); err != nil {
		if k8serrors.IsNotFound(err) {
			return s.k8s.Create(ctx, cm)
		}
		return err
	}
	existing.Data = data
	return s.k8s.Update(ctx, &existing)
}

func validateCategory(c string) error {
	if c != CategoryConfigs && c != CategoryScenarios {
		return fmt.Errorf("unknown category %q (want %q or %q)", c, CategoryConfigs, CategoryScenarios)
	}
	return nil
}

func validateName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\\..") && name != filepath.Base(name) {
		return fmt.Errorf("invalid file name %q", name)
	}
	if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
		return fmt.Errorf("file name must end with .yaml or .yml")
	}
	return nil
}
