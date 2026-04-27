package steps

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	backupPollPeriod = 10 * time.Second
	backupTimeout    = 10 * time.Minute
)

// TakeBackup creates a CNPG volumeSnapshot Backup named after the run ID and
// waits until it reaches a terminal state (completed or failed).
func TakeBackup(ctx context.Context, rc *RunContext) error {
	const stepName = "take-backup"

	// Include the session ID in the name so re-running the same scenario never
	// collides with a VolumeSnapshot left by a previous session. Without a unique
	// suffix, CNPG's reconciler finds the old snapshot and skips pg_backup_start,
	// then fails in finalize because no backup session is active.
	backupName := fmt.Sprintf("bench-%s-%s", rc.RunSpec.ID, rc.SessionID)
	logStep(rc.Log, stepName, fmt.Sprintf("creating backup %q", backupName))

	// Drain non-terminal Backup CRDs so CNPG's internal backup tracking is clear
	// before we create the new Backup object.
	if err := drainRunningBackups(ctx, rc, stepName); err != nil {
		return fmt.Errorf("drain running backups: %w", err)
	}

	backup := &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: cnpgNamespace,
		},
		Spec: cnpgv1.BackupSpec{
			Method: cnpgv1.BackupMethodVolumeSnapshot,
			Cluster: cnpgv1.LocalObjectReference{
				Name: cnpgClusterName,
			},
		},
	}

	if err := rc.K8sClient.Create(ctx, backup); err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	rc.Logf("[%s] backup %q created; waiting for completion", stepName, backupName)

	return waitForBackup(ctx, rc, stepName, backupName)
}

// drainRunningBackups deletes every backup in a non-terminal phase and polls
// until none remain. This clears CNPG's internal "current backup" tracking so
// the next Create does not hit the stale-empty-name conflict.
func drainRunningBackups(ctx context.Context, rc *RunContext, stepName string) error {
	isTerminal := func(phase cnpgv1.BackupPhase) bool {
		return phase == cnpgv1.BackupPhaseCompleted || phase == cnpgv1.BackupPhaseFailed
	}

	var list cnpgv1.BackupList
	if err := rc.K8sClient.List(ctx, &list, client.InNamespace(cnpgNamespace)); err != nil {
		return err
	}
	for i := range list.Items {
		b := &list.Items[i]
		if isTerminal(b.Status.Phase) {
			continue
		}
		rc.Logf("[%s] deleting non-terminal backup %q (phase=%s)", stepName, b.Name, b.Status.Phase)
		if err := rc.K8sClient.Delete(ctx, b); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("delete backup %s: %w", b.Name, err)
		}
	}

	// Poll until all remaining backups are in a terminal phase.
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		var poll cnpgv1.BackupList
		if err := rc.K8sClient.List(ctx, &poll, client.InNamespace(cnpgNamespace)); err != nil {
			rc.Logf("[%s] warn: list backups: %v", stepName, err)
			continue
		}
		allDone := true
		for _, b := range poll.Items {
			if !isTerminal(b.Status.Phase) {
				allDone = false
				rc.Logf("[%s] waiting for backup %q to terminate (phase=%s)", stepName, b.Name, b.Status.Phase)
				break
			}
		}
		if allDone {
			return nil
		}
	}
	return fmt.Errorf("timeout waiting for non-terminal backups to drain")
}


func waitForBackup(ctx context.Context, rc *RunContext, stepName, backupName string) error {
	deadline := time.Now().Add(backupTimeout)
	key := types.NamespacedName{Name: backupName, Namespace: cnpgNamespace}

	for time.Now().Before(deadline) {
		var b cnpgv1.Backup
		if err := rc.K8sClient.Get(ctx, key, &b); err != nil {
			rc.Logf("[%s] warn: get backup: %v", stepName, err)
		} else {
			phase := string(b.Status.Phase)
			rc.Logf("[%s] backup phase: %s", stepName, phase)
			switch b.Status.Phase {
			case cnpgv1.BackupPhaseCompleted:
				rc.Logf("[%s] backup %q completed", stepName, backupName)
				return nil
			case cnpgv1.BackupPhaseFailed:
				return fmt.Errorf("backup %q failed: %s", backupName, b.Status.Error)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backupPollPeriod):
		}
	}

	return fmt.Errorf("timeout after %s waiting for backup %q", backupTimeout, backupName)
}

