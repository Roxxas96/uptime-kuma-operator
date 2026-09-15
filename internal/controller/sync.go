package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

// desiredHash returns a stable fingerprint of desired (the derived monitor
// set for an Ingress/HTTPRoute — a function of both .spec and the override
// annotations). Reconcilers compare this against the SyncedHash annotation
// from the last successful sync to decide whether to call syncMonitors at
// all.
//
// This matters because kuma.Client.Upsert (editMonitor) restarts the
// monitor's check timer on Kuma's side even when the payload is byte-for-byte
// unchanged. Without this guard, syncMonitors would run on every reconcile
// — including ones triggered by something unrelated to this monitor's own
// configuration (annotation churn from other tooling, an informer resync, an
// error-triggered requeue) — and a resource reconciled more often than its
// configured check interval would never complete more than one check cycle.
func desiredHash(desired []derive.DesiredMonitor) (string, error) {
	b, err := json.Marshal(desired)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// syncMonitors upserts one monitor per desired host (reusing existingIDs[host]
// when present) and deletes any host present in existingIDs but absent from
// desired. It returns the new host -> id map.
//
// It is shared by the Ingress and HTTPRoute reconcilers: both work purely in
// terms of derive.DesiredMonitor and the host -> id annotation map.
func syncMonitors(ctx context.Context, kc kuma.Client, desired []derive.DesiredMonitor, existingIDs map[string]string) (map[string]string, error) {
	log := logf.FromContext(ctx)
	wanted := map[string]bool{}
	newIDs := map[string]string{}

	for _, dm := range desired {
		wanted[dm.Host] = true
		var id int64
		if idStr, ok := existingIDs[dm.Host]; ok {
			parsed, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				log.Error(err, "stored Kuma monitor ID is not an integer, creating a new monitor",
					"host", dm.Host, "monitorID", idStr)
			} else {
				id = parsed
			}
		}
		newID, err := kc.Upsert(ctx, id, dm.Spec)
		if err != nil {
			return nil, err
		}
		if id == 0 {
			log.Info("created Kuma monitor", "host", dm.Host, "monitorID", newID)
		} else {
			log.Info("updated Kuma monitor", "host", dm.Host, "monitorID", newID)
		}
		newIDs[dm.Host] = strconv.FormatInt(newID, 10)
	}

	for host, idStr := range existingIDs {
		if wanted[host] {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			log.Error(err, "stored Kuma monitor ID is not an integer, skipping delete",
				"host", host, "monitorID", idStr)
			continue
		}
		if err := kc.Delete(ctx, id); err != nil {
			return nil, err
		}
		log.Info("deleted Kuma monitor", "host", host, "monitorID", id)
	}

	return newIDs, nil
}

// specsMatch reports whether every host in existingIDs still has a Kuma
// monitor that both exists and matches the configuration currently desired
// for it (per kuma.Equivalent), against liveSpecs (the current Kuma
// monitor set from kuma.Client.ExistingSpecs). An unparseable or missing ID
// counts as drift, so a corrupted annotation or an out-of-band deletion
// triggers recreation rather than being silently treated as fine. A host
// present in existingIDs but no longer in desired is ignored here — the
// normal sync path handles removal, not this drift check.
func specsMatch(desired []derive.DesiredMonitor, existingIDs map[string]string, liveSpecs map[int64]kuma.MonitorSpec) bool {
	byHost := make(map[string]derive.DesiredMonitor, len(desired))
	for _, dm := range desired {
		byHost[dm.Host] = dm
	}
	for host, idStr := range existingIDs {
		dm, ok := byHost[host]
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return false
		}
		live, ok := liveSpecs[id]
		if !ok || !kuma.Equivalent(dm.Spec, live) {
			return false
		}
	}
	return true
}

// reconcileDrift corrects only the monitors in existingIDs that have
// drifted from liveSpecs — either missing entirely (deleted out-of-band)
// or present with a configuration that no longer matches desired (edited
// out-of-band) — leaving every other host's monitor untouched. It returns
// the updated host -> id map.
//
// This is deliberately narrower than syncMonitors: it runs on the "nothing
// changed since the last successful sync" path, where the desired
// configuration for hosts that already match is already correct — calling
// kuma.Client.Upsert on them anyway would be the exact redundant-editMonitor
// bug this whole mechanism exists to avoid. A missing monitor gets a fresh
// Upsert(id=0, ...); a present-but-drifted one gets Upsert(id=existingID,
// ...) to correct it in place.
func reconcileDrift(ctx context.Context, kc kuma.Client, desired []derive.DesiredMonitor, existingIDs map[string]string, liveSpecs map[int64]kuma.MonitorSpec) (map[string]string, error) {
	log := logf.FromContext(ctx)
	byHost := make(map[string]derive.DesiredMonitor, len(desired))
	for _, dm := range desired {
		byHost[dm.Host] = dm
	}

	newIDs := make(map[string]string, len(existingIDs))
	for host, idStr := range existingIDs {
		newIDs[host] = idStr
	}

	for host, idStr := range existingIDs {
		dm, ok := byHost[host]
		if !ok {
			continue // no longer desired either; the normal sync path handles removal
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		live, exists := liveSpecs[id]
		if err == nil && exists && kuma.Equivalent(dm.Spec, live) {
			continue // still exists and matches, nothing to do
		}
		upsertID := id
		missing := err != nil || !exists
		if missing {
			upsertID = 0 // missing or corrupted id — create fresh
		}
		newID, err := kc.Upsert(ctx, upsertID, dm.Spec)
		if err != nil {
			return nil, err
		}
		if missing {
			log.Info("recreated Kuma monitor deleted out-of-band", "host", host, "monitorID", newID)
		} else {
			log.Info("corrected Kuma monitor configuration drifted out-of-band", "host", host, "monitorID", newID)
		}
		newIDs[host] = strconv.FormatInt(newID, 10)
	}
	return newIDs, nil
}

// deleteAllMonitors deletes every Kuma monitor recorded in ann's monitor-ids
// annotation.
func deleteAllMonitors(ctx context.Context, kc kuma.Client, ann map[string]string) error {
	log := logf.FromContext(ctx)
	ids, err := annotations.ParseMonitorIDs(ann)
	if err != nil {
		return err
	}
	for host, idStr := range ids {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			log.Error(err, "stored Kuma monitor ID is not an integer, skipping delete",
				"host", host, "monitorID", idStr)
			continue
		}
		if err := kc.Delete(ctx, id); err != nil {
			return err
		}
		log.Info("deleted Kuma monitor", "host", host, "monitorID", id)
	}
	return nil
}

// persistMonitorIDs writes newIDs and hash into obj's annotations and adds or
// removes the operator finalizer, retrying on conflict. hash is the
// desiredHash of what was just synced ("" clears it, e.g. on opt-out — there
// is no "last synced state" to remember once a resource is no longer ours).
//
// The retry matters: the Kuma monitors have already been created by the time
// this runs, so losing the write to a routine conflict (another controller
// touching .status, a concurrent reconcile) would make the next reconcile
// upsert with id=0 and orphan the monitors it just created.
func persistMonitorIDs(ctx context.Context, c client.Client, obj client.Object, newIDs map[string]string, wantFinalizer bool, hash string) error {
	key := client.ObjectKeyFromObject(obj)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Get(ctx, key, obj); err != nil {
			return err
		}
		ann := obj.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		annotations.SetMonitorIDs(ann, newIDs)
		annotations.SetSyncedHash(ann, hash)
		obj.SetAnnotations(ann)
		if wantFinalizer {
			controllerutil.AddFinalizer(obj, annotations.Finalizer)
		} else {
			controllerutil.RemoveFinalizer(obj, annotations.Finalizer)
		}
		return c.Update(ctx, obj)
	})
}

// recordSyncFailure emits a Warning Event for a failed Kuma sync, as required
// by the design spec's error-handling section. It tolerates a nil recorder so
// reconcilers stay usable without a manager.
func recordSyncFailure(rec record.EventRecorder, obj client.Object, err error) {
	if rec == nil {
		return
	}
	rec.Event(obj, corev1.EventTypeWarning, "SyncFailed", err.Error())
}
