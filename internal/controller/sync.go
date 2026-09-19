package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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

// resolveReferences resolves notification channel names and a group name
// into their Kuma IDs. A name that doesn't exist in Kuma is a hard error —
// callers should treat it exactly like any other sync failure (record a
// SyncFailed Event, return the error for the usual requeue-with-backoff).
// (nil, nil, nil) is returned when there's nothing to resolve, so callers
// can skip the Kuma round-trip entirely for the common case of no
// notifications/group configured.
func resolveReferences(ctx context.Context, kc kuma.Client, notificationNames []string, groupName string) ([]int64, *int64, error) {
	var notificationIDs []int64
	if len(notificationNames) > 0 {
		known, err := kc.Notifications(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve notifications: %w", err)
		}
		// Dedupe and sort: Kuma stores a monitor's notification associations
		// as a set, so a repeated name ([1,1]) would never compare equal to
		// what Kuma returns ([1]) and would re-Upsert forever; and a stable
		// order keeps desiredHash from changing when the same names are
		// merely reordered.
		seen := make(map[int64]bool, len(notificationNames))
		notificationIDs = make([]int64, 0, len(notificationNames))
		for _, name := range notificationNames {
			id, ok := known[name]
			if !ok {
				return nil, nil, fmt.Errorf("notification channel %q not found in Kuma", name)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			notificationIDs = append(notificationIDs, id)
		}
		sort.Slice(notificationIDs, func(i, j int) bool { return notificationIDs[i] < notificationIDs[j] })
	}

	var groupID *int64
	if groupName != "" {
		id, found, err := kc.FindGroup(ctx, groupName)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve group %q: %w", groupName, err)
		}
		if !found {
			return nil, nil, fmt.Errorf("group %q not found in Kuma", groupName)
		}
		groupID = &id
	}

	return notificationIDs, groupID, nil
}

// resolveSecret reads the value at ref's key from the Secret named ref.Name
// in namespace. Returns "" with no error if ref is nil (field not configured).
func resolveSecret(ctx context.Context, c client.Client, namespace string, ref *corev1.SecretKeySelector) (string, error) {
	if ref == nil {
		return "", nil
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return "", fmt.Errorf("resolve secret %s/%s: %w", namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("resolve secret %s/%s: key %q not found", namespace, ref.Name, ref.Key)
	}
	return string(value), nil
}

// syncTags reconciles monitorID's Kuma tag associations to match tagNames
// exactly, creating any tag that doesn't already exist. This runs entirely
// outside Equivalent/Upsert: Kuma manages monitor-tag associations via
// separate calls, not as part of a monitor's own create/update payload, so
// it needs its own step after every successful sync (including the
// "nothing else changed" drift-check path, since tags can drift out of
// band on their own).
func syncTags(ctx context.Context, kc kuma.Client, monitorID int64, tagNames []string) error {
	if len(tagNames) == 0 {
		return kc.SetMonitorTags(ctx, monitorID, nil)
	}
	known, err := kc.Tags(ctx)
	if err != nil {
		return fmt.Errorf("sync tags: list existing: %w", err)
	}
	ids := make([]int64, 0, len(tagNames))
	for _, name := range tagNames {
		id, ok := known[name]
		if !ok {
			id, err = kc.CreateTag(ctx, name)
			if err != nil {
				return fmt.Errorf("sync tags: create %q: %w", name, err)
			}
		}
		ids = append(ids, id)
	}
	return kc.SetMonitorTags(ctx, monitorID, ids)
}

// mergeTags unions any number of tag-name sources — operator-wide
// DEFAULT_TAGS, a resource's own explicit tags, and (Phase 3)
// label-derived tags — deduplicating by name so a tag listed in more than
// one source isn't resolved/created twice. Order is source-order, first
// occurrence wins — cosmetic only, since syncTags resolves names to IDs
// and Kuma tracks tag associations as a set.
func mergeTags(sources ...[]string) []string {
	seen := make(map[string]bool)
	var merged []string
	for _, src := range sources {
		for _, name := range src {
			if !seen[name] {
				seen[name] = true
				merged = append(merged, name)
			}
		}
	}
	return merged
}

// deriveLabelTags returns the Kuma tag names derived from labels for
// Phase 3's operator-wide label-tag inference: for every label whose key
// fully matches at least one of patterns, "key=value" is included. patterns
// are assumed already anchored (config.Load compiles them as ^(?:...)$) —
// this function does no anchoring of its own. Order is unspecified; callers
// that need determinism should sort, same as any other tag-name slice fed
// into mergeTags/syncTags.
func deriveLabelTags(labels map[string]string, patterns []*regexp.Regexp) []string {
	if len(patterns) == 0 {
		return nil
	}
	var tags []string
	for key, value := range labels {
		for _, pattern := range patterns {
			if pattern.MatchString(key) {
				tags = append(tags, key+"="+value)
				break
			}
		}
	}
	return tags
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
