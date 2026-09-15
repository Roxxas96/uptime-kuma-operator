package controller

import (
	"context"
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
	}
	return nil
}

// persistMonitorIDs writes newIDs into obj's monitor-ids annotation and adds or
// removes the operator finalizer, retrying on conflict.
//
// The retry matters: the Kuma monitors have already been created by the time
// this runs, so losing the write to a routine conflict (another controller
// touching .status, a concurrent reconcile) would make the next reconcile
// upsert with id=0 and orphan the monitors it just created.
func persistMonitorIDs(ctx context.Context, c client.Client, obj client.Object, newIDs map[string]string, wantFinalizer bool) error {
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
