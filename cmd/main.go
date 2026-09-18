package main

import (
	"context"
	"os"

	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	uptimekumaiov1alpha1 "uptime-kuma-operator/api/v1alpha1"
	"uptime-kuma-operator/internal/config"
	"uptime-kuma-operator/internal/controller"
	"uptime-kuma-operator/internal/kuma"
)

var scheme = clientgoscheme.Scheme

// zapLevels maps config.Config.LogLevel's accepted values to zap levels.
// "debug" is what makes the operator's V(1) reconcile-detail logs (what
// triggered a reconcile, which decision branch was taken) visible, on top
// of the always-on Info-level create/update/delete action logs.
var zapLevels = map[string]zapcore.Level{
	"debug": zapcore.DebugLevel,
	"info":  zapcore.InfoLevel,
	"warn":  zapcore.WarnLevel,
	"error": zapcore.ErrorLevel,
}

func init() {
	utilruntime.Must(networkingv1.AddToScheme(scheme))
	utilruntime.Must(uptimekumaiov1alpha1.AddToScheme(scheme))
	// gatewayv1.Install is called conditionally in main(), only if the
	// HTTPRoute CRD is actually present in the target cluster.
}

func main() {
	// Default to Info level until config.Load resolves LogLevel below — this
	// only affects the brief window before that, e.g. a config error.
	ctrl.SetLogger(zap.New())
	log := ctrl.Log.WithName("setup")

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Error(err, "invalid configuration")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.Level(zapLevels[cfg.LogLevel])))
	log = ctrl.Log.WithName("setup")

	ctx := context.Background()
	kumaClient, err := kuma.NewClient(ctx, cfg.KumaURL, cfg.KumaUsername, cfg.KumaPassword)
	if err != nil {
		log.Error(err, "unable to connect to Uptime Kuma")
		os.Exit(1)
	}

	restCfg := ctrl.GetConfigOrDie()

	// Secrets are read one-by-one by name (resolveSecret), never listed. Left
	// cached, the first credential ref would start a cluster- (or namespace-)
	// wide Secret LIST+WATCH held in memory for the process lifetime; reads go
	// straight to the API server instead.
	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&corev1.Secret{}},
			},
		},
	}
	if !cfg.WatchAll {
		nsCache := map[string]cache.Config{}
		for _, ns := range cfg.WatchNamespaces {
			nsCache[ns] = cache.Config{}
		}
		mgrOpts.Cache = cache.Options{DefaultNamespaces: nsCache}
	}

	mgr, err := ctrl.NewManager(restCfg, mgrOpts)
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	recorder := mgr.GetEventRecorderFor("uptime-kuma-operator") //nolint:staticcheck // GetEventRecorder returns a different EventRecorder type (events.k8s.io/v1); migrating also needs new RBAC verbs and reconciler field types — tracked as a follow-up, not done here

	if err := (&controller.IngressReconciler{
		Client: mgr.GetClient(), Kuma: kumaClient, OptInByDefault: cfg.OptInByDefault,
		Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval, DefaultTags: cfg.DefaultTags,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create Ingress controller")
		os.Exit(1)
	}

	if err := (&controller.MonitorReconciler{
		Client: mgr.GetClient(), Kuma: kumaClient, Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval,
		DefaultTags: cfg.DefaultTags,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create Monitor controller")
		os.Exit(1)
	}

	if httpRouteCRDInstalled(restCfg) {
		utilruntime.Must(gatewayv1.Install(scheme))
		if err := (&controller.HTTPRouteReconciler{
			Client: mgr.GetClient(), Kuma: kumaClient, OptInByDefault: cfg.OptInByDefault,
			Recorder: recorder, DriftCheckInterval: cfg.DriftCheckInterval, DefaultTags: cfg.DefaultTags,
		}).SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create HTTPRoute controller")
			os.Exit(1)
		}
	} else {
		log.Info("HTTPRoute CRD not found in cluster, skipping HTTPRoute controller")
	}

	log.Info("starting manager", "watchNamespaces", cfg.WatchNamespaces, "watchAll", cfg.WatchAll,
		"optInByDefault", cfg.OptInByDefault, "driftCheckInterval", cfg.DriftCheckInterval, "logLevel", cfg.LogLevel,
		"defaultTags", cfg.DefaultTags)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited with error")
		os.Exit(1)
	}
}

// httpRouteCRDInstalled checks whether the HTTPRoute CRD is registered in
// the target cluster, so the operator doesn't crash-loop in clusters
// without Gateway API installed.
func httpRouteCRDInstalled(restCfg *rest.Config) bool {
	dc, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return false
	}
	resources, err := dc.ServerResourcesForGroupVersion(gatewayv1.GroupVersion.String())
	if err != nil {
		return false
	}
	for _, res := range resources.APIResources {
		if res.Kind == "HTTPRoute" {
			return true
		}
	}
	return false
}
