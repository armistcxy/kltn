package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	configMapName      = "auto-run-matrix"
	configMapNamespace = "default"
	configMapKey       = "matrix.yaml"
)

// NewStore loads the matrix from filePath and initialises all run states to queued.
func NewStore(filePath string) (*Store, error) {
	s := &Store{
		filePath: filePath,
		states:   make(map[string]*RunState),
	}
	if err := s.loadFile(filePath); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read matrix file: %w", err)
	}
	var mf MatrixFile
	if err := sigsyaml.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("parse matrix yaml: %w", err)
	}
	s.file = mf
	s.order = make([]string, 0, len(mf.Runs))
	for _, r := range mf.Runs {
		s.order = append(s.order, r.ID)
		if _, exists := s.states[r.ID]; !exists {
			s.states[r.ID] = &RunState{
				Status: StatusQueued,
				Steps:  defaultSteps(),
			}
		}
	}
	return nil
}

func defaultSteps() []StepInfo {
	names := []string{"reset-cluster", "deploy-controller", "run-loadgen", "collect-metrics", "upload-gcs"}
	steps := make([]StepInfo, len(names))
	for i, n := range names {
		steps[i] = StepInfo{Name: n, Status: StepPending}
	}
	return steps
}

// Defaults returns a copy of the global defaults.
func (s *Store) Defaults() GlobalDefaults {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.file.Defaults
}

// Runs returns a snapshot of all runs (spec + state) in order.
func (s *Store) Runs() []Run {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runs := make([]Run, 0, len(s.order))
	specByID := make(map[string]RunSpec, len(s.file.Runs))
	for _, r := range s.file.Runs {
		specByID[r.ID] = r
	}
	for _, id := range s.order {
		spec, ok := specByID[id]
		if !ok {
			continue
		}
		state := s.states[id]
		if state == nil {
			state = &RunState{Status: StatusQueued, Steps: defaultSteps()}
		}
		runs = append(runs, Run{RunSpec: spec, RunState: *state})
	}
	return runs
}

// GetSpec returns the spec of a run by ID.
func (s *Store) GetSpec(id string) (RunSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.file.Runs {
		if r.ID == id {
			return r, true
		}
	}
	return RunSpec{}, false
}

// SetStatus updates the status of a run.
func (s *Store) SetStatus(id string, status RunStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[id]; ok {
		st.Status = status
	}
}

// SetState replaces the full state of a run.
func (s *Store) SetState(id string, state RunState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[id] = &state
}

// GetState returns a copy of the runtime state for a run.
func (s *Store) GetState(id string) (RunState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.states[id]
	if !ok {
		return RunState{}, false
	}
	return *st, true
}

// SetStepStatus updates a single step's status within a run.
func (s *Store) SetStepStatus(runID, stepName string, status StepStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[runID]
	if !ok {
		return
	}
	for i := range st.Steps {
		if st.Steps[i].Name == stepName {
			st.Steps[i].Status = status
			return
		}
	}
}

// NextQueued returns the ID of the first queued run, or "" if none.
func (s *Store) NextQueued() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, id := range s.order {
		if st, ok := s.states[id]; ok && st.Status == StatusQueued {
			return id
		}
	}
	return ""
}

// ResetQueued resets all failed/succeeded runs back to queued.
func (s *Store) ResetQueued() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.states {
		if st.Status == StatusFailed || st.Status == StatusSuccess {
			s.states[id] = &RunState{Status: StatusQueued, Steps: defaultSteps()}
		}
	}
}

// ReplaceMatrix replaces the run list + defaults, preserving existing state for unchanged IDs.
func (s *Store) ReplaceMatrix(mf MatrixFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = mf
	newOrder := make([]string, 0, len(mf.Runs))
	for _, r := range mf.Runs {
		newOrder = append(newOrder, r.ID)
		if _, exists := s.states[r.ID]; !exists {
			s.states[r.ID] = &RunState{Status: StatusQueued, Steps: defaultSteps()}
		}
	}
	s.order = newOrder
}

// UpdateSpec updates the spec of an existing run (must not be running).
func (s *Store) UpdateSpec(id string, updated RunSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[id]; ok && st.Status == StatusRunning {
		return fmt.Errorf("run %q is currently running", id)
	}
	for i, r := range s.file.Runs {
		if r.ID == id {
			s.file.Runs[i] = updated
			return nil
		}
	}
	return fmt.Errorf("run %q not found", id)
}

// DeleteRun removes a run (only if queued or failed).
func (s *Store) DeleteRun(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[id]; ok {
		if st.Status == StatusRunning {
			return fmt.Errorf("run %q is currently running", id)
		}
	}
	s.file.Runs = slices.DeleteFunc(s.file.Runs, func(r RunSpec) bool { return r.ID == id })
	s.order = slices.DeleteFunc(s.order, func(oid string) bool { return oid == id })
	delete(s.states, id)
	return nil
}

// MoveAfter moves run `id` to immediately after run `afterID`.
// If afterID == "" the run is moved to the front.
func (s *Store) MoveAfter(id, afterID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.order, id) {
		return fmt.Errorf("run %q not found", id)
	}
	if afterID != "" && !slices.Contains(s.order, afterID) {
		return fmt.Errorf("after run %q not found", afterID)
	}
	// Remove id from current position.
	s.order = slices.DeleteFunc(s.order, func(oid string) bool { return oid == id })
	// Insert after afterID.
	if afterID == "" {
		s.order = append([]string{id}, s.order...)
	} else {
		for i, oid := range s.order {
			if oid == afterID {
				s.order = slices.Insert(s.order, i+1, id)
				return nil
			}
		}
		// afterID disappeared; append to end.
		s.order = append(s.order, id)
	}
	return nil
}

// UpdateDefaults replaces the global defaults.
func (s *Store) UpdateDefaults(d GlobalDefaults) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file.Defaults = d
}

// ToMatrixFile returns a snapshot of the current MatrixFile (spec only).
func (s *Store) ToMatrixFile() MatrixFile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return runs in current order.
	specByID := make(map[string]RunSpec, len(s.file.Runs))
	for _, r := range s.file.Runs {
		specByID[r.ID] = r
	}
	ordered := make([]RunSpec, 0, len(s.order))
	for _, id := range s.order {
		if r, ok := specByID[id]; ok {
			ordered = append(ordered, r)
		}
	}
	return MatrixFile{Defaults: s.file.Defaults, Runs: ordered}
}

// SaveToFile writes the current matrix (spec only) back to filePath.
func (s *Store) SaveToFile() error {
	mf := s.ToMatrixFile()
	data, err := sigsyaml.Marshal(mf)
	if err != nil {
		return fmt.Errorf("marshal matrix: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0o644)
}

// SyncFromConfigMap reads the matrix ConfigMap and replaces the in-memory state.
func (s *Store) SyncFromConfigMap(ctx context.Context, k8s client.Client) error {
	var cm corev1.ConfigMap
	key := client.ObjectKey{Namespace: configMapNamespace, Name: configMapName}
	if err := k8s.Get(ctx, key, &cm); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil // nothing to sync
		}
		return err
	}
	yamlData, ok := cm.Data[configMapKey]
	if !ok {
		return nil
	}
	var mf MatrixFile
	if err := sigsyaml.Unmarshal([]byte(yamlData), &mf); err != nil {
		return fmt.Errorf("parse configmap matrix: %w", err)
	}
	s.ReplaceMatrix(mf)
	return nil
}

// PersistToConfigMap writes the current matrix to a Kubernetes ConfigMap so it
// survives pod restarts.
func (s *Store) PersistToConfigMap(ctx context.Context, k8s client.Client) error {
	mf := s.ToMatrixFile()
	data, err := sigsyaml.Marshal(mf)
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: configMapNamespace,
		},
		Data: map[string]string{configMapKey: string(data)},
	}
	var existing corev1.ConfigMap
	key := client.ObjectKey{Namespace: configMapNamespace, Name: configMapName}
	if err := k8s.Get(ctx, key, &existing); err != nil {
		if k8serrors.IsNotFound(err) {
			return k8s.Create(ctx, cm)
		}
		return err
	}
	existing.Data = cm.Data
	return k8s.Update(ctx, &existing)
}

// MatrixJSON returns the full matrix (spec + states) as JSON bytes for the API.
func (s *Store) MatrixJSON() ([]byte, error) {
	type response struct {
		Defaults GlobalDefaults `json:"defaults"`
		Runs     []Run          `json:"runs"`
	}
	resp := response{
		Defaults: s.Defaults(),
		Runs:     s.Runs(),
	}
	return json.Marshal(resp)
}
