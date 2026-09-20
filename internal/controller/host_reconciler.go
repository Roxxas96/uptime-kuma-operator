package controller

import (
	"context"
	"regexp"
	"strconv"
	"time"

	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"uptime-kuma-operator/internal/annotations"
	"uptime-kuma-operator/internal/derive"
	"uptime-kuma-operator/internal/kuma"
)

// hostReconcilerParams bundles what's identical across the Ingress and
// HTTPRoute reconcilers — everything reconcileHostBasedResource needs
// except the resource itself and how to derive its desired monitors.
type hostReconcilerParams struct {
	Client             client.Client
	Kuma               kuma.Client
	OptInByDefault     bool
	Recorder           record.EventRecorder
	DriftCheckInterval time.Duration
	DefaultTags        []string
	LabelTagPatterns   []*regexp.Regexp
}

// reconcileHostBasedResource implements the reconcile logic shared by the
// Ingress and HTTPRoute reconcilers: deletion + finalizer cleanup, opt-in/
// opt-out handling, hash-based skip with drift-check, full sync, and tag
// sync. obj must already be Get'd from the API server by the caller (which
// needs the concrete type anyway, both to call Client.Get and to build
// deriveMonitors). kind is used only in log messages ("Ingress" /
// "HTTPRoute"). deriveMonitors builds the desired per-host monitor set
// from obj's routing rules and the given override annotations —
// derive.IngressMonitors / derive.HTTPRouteMonitors / derive.ServiceMonitors,
// bound to obj by the caller's closure. Only the Service closure can
// actually return a non-nil error (ambiguous port, missing required
// per-type field, unknown type) — Ingress/HTTPRoute always return nil.
func reconcileHostBasedResource(
	ctx context.Context,
	p hostReconcilerParams,
	obj client.Object,
	kind string,
	deriveMonitors func(annotations.Overrides) ([]derive.DesiredMonitor, error),
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !obj.GetDeletionTimestamp().IsZero() {
		log.V(1).Info(kind + " marked for deletion, deleting its Kuma monitors")
		if err := deleteAllMonitors(ctx, p.Kuma, obj.GetAnnotations()); err != nil {
			recordSyncFailure(p.Recorder, obj, err)
			return ctrl.Result{}, err
		}
		if controllerutil.ContainsFinalizer(obj, annotations.Finalizer) {
			controllerutil.RemoveFinalizer(obj, annotations.Finalizer)
			if err := p.Client.Update(ctx, obj); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !annotations.ShouldSync(p.OptInByDefault, obj.GetAnnotations()) {
		existingIDs, err := annotations.ParseMonitorIDs(obj.GetAnnotations())
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(existingIDs) == 0 && !controllerutil.ContainsFinalizer(obj, annotations.Finalizer) {
			log.V(1).Info(kind+" not opted in and has no tracked monitors, nothing to do", "optInByDefault", p.OptInByDefault)
			return ctrl.Result{}, nil
		}
		log.V(1).Info(kind+" opted out, deleting its Kuma monitors", "optInByDefault", p.OptInByDefault)
		if err := deleteAllMonitors(ctx, p.Kuma, obj.GetAnnotations()); err != nil {
			recordSyncFailure(p.Recorder, obj, err)
			return ctrl.Result{}, err
		}
		// Drop the monitor-ids annotation *and* the finalizer: an opted-out
		// resource is no longer ours, and a lingering finalizer would block
		// its deletion forever if the operator is uninstalled.
		return ctrl.Result{}, persistMonitorIDs(ctx, p.Client, obj, nil, false, "")
	}

	ov, err := annotations.ParseOverrides(obj.GetAnnotations())
	if err != nil {
		return ctrl.Result{}, err
	}
	existingIDs, err := annotations.ParseMonitorIDs(obj.GetAnnotations())
	if err != nil {
		return ctrl.Result{}, err
	}

	desired, err := deriveMonitors(ov)
	if err != nil {
		recordSyncFailure(p.Recorder, obj, err)
		return ctrl.Result{}, err
	}
	notificationIDs, groupID, err := resolveReferences(ctx, p.Kuma, ov.Notifications, ov.Group)
	if err != nil {
		recordSyncFailure(p.Recorder, obj, err)
		return ctrl.Result{}, err
	}
	for i := range desired {
		desired[i].Spec.NotificationIDs = notificationIDs
		desired[i].Spec.GroupID = groupID
	}
	hash, err := desiredHash(desired)
	if err != nil {
		return ctrl.Result{}, err
	}

	tags := mergeTags(p.DefaultTags, ov.Tags, deriveLabelTags(obj.GetLabels(), p.LabelTagPatterns))

	if hash == annotations.ParseSyncedHash(obj.GetAnnotations()) {
		// Nothing relevant (routing rules or override annotations) changed
		// since the last successful sync — skip the Kuma round-trip. See
		// desiredHash's doc comment for why this matters. But "skip" must
		// not mean "forever": verify Kuma still has what we think it has,
		// and that it's still configured the way we want, since a monitor
		// deleted or edited out-of-band (e.g. manually in the Kuma UI)
		// would otherwise never be noticed or corrected.
		log.V(1).Info("desired configuration unchanged since last sync, checking Kuma for drift", "hash", hash)
		liveSpecs, err := p.Kuma.ExistingSpecs(ctx)
		if err != nil {
			return ctrl.Result{}, err
		}
		if specsMatch(desired, existingIDs, liveSpecs) {
			if err := syncTagsForHosts(ctx, p.Kuma, p.Recorder, obj, existingIDs, tags); err != nil {
				return ctrl.Result{}, err
			}
			log.V(1).Info("Kuma monitors match desired state, nothing to do", "hosts", len(desired))
			return ctrl.Result{RequeueAfter: p.DriftCheckInterval}, nil
		}
		log.V(1).Info("Kuma monitors drifted from desired state, correcting")
		newIDs, err := reconcileDrift(ctx, p.Kuma, desired, existingIDs, liveSpecs)
		if err != nil {
			recordSyncFailure(p.Recorder, obj, err)
			return ctrl.Result{}, err
		}
		// Persist the monitor IDs *before* syncing tags: the monitors already
		// exist in Kuma at this point, so a tag-sync failure that skipped this
		// write would leave the next reconcile with no record of them and
		// upsert duplicates with id=0.
		if err := persistMonitorIDs(ctx, p.Client, obj, newIDs, true, hash); err != nil {
			return ctrl.Result{}, err
		}
		if err := syncTagsForHosts(ctx, p.Kuma, p.Recorder, obj, newIDs, tags); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: p.DriftCheckInterval}, nil
	}

	log.V(1).Info("desired configuration changed, syncing", "oldHash", annotations.ParseSyncedHash(obj.GetAnnotations()), "newHash", hash, "hosts", len(desired))
	newIDs, err := syncMonitors(ctx, p.Kuma, desired, existingIDs)
	if err != nil {
		recordSyncFailure(p.Recorder, obj, err)
		return ctrl.Result{}, err
	}
	// Persist first, sync tags second — see the drift-correction branch above.
	if err := persistMonitorIDs(ctx, p.Client, obj, newIDs, true, hash); err != nil {
		return ctrl.Result{}, err
	}
	if err := syncTagsForHosts(ctx, p.Kuma, p.Recorder, obj, newIDs, tags); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: p.DriftCheckInterval}, nil
}

// syncTagsForHosts syncs Kuma tags for every host's monitor in ids to
// tagNames. An id that fails to parse as int64 is skipped rather than
// treated as an error: every value in ids was written by
// strconv.FormatInt a few lines earlier in this same reconcile (by
// syncMonitors or reconcileDrift), so a parse failure here is
// unreachable in practice — defensive only, not a real error path.
func syncTagsForHosts(ctx context.Context, kc kuma.Client, rec record.EventRecorder, obj client.Object, ids map[string]string, tagNames []string) error {
	for _, idStr := range ids {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if err := syncTags(ctx, kc, id, tagNames); err != nil {
			recordSyncFailure(rec, obj, err)
			return err
		}
	}
	return nil
}
