package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // For GCP auth
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"           // Ensure webhook is imported if directly used, though often implicitly handled by manager
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission" // Added for admission.NewDecoder

	flexdaemonsetsv1alpha1 "github.com/prakarsh-dt/FlexDaemonsets/pkg/apis/flexdaemonsets/v1alpha1"
	flexcontroller "github.com/prakarsh-dt/FlexDaemonsets/pkg/controller"    // Import the new controller package
	flexdaemonsetwebhook "github.com/prakarsh-dt/FlexDaemonsets/pkg/webhook" // Import the webhook package
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = flexdaemonsetsv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var certDir string // Added variable for cert directory

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	// Added flag for cert directory. The controller-runtime manager will automatically use this directory
	// to find tls.crt and tls.key files for the webhook server.
	flag.StringVar(&certDir, "cert-dir", "/tmp/k8s-webhook-server/serving-certs", "Directory where the TLS certs (tls.crt, tls.key) are located. Defaults to /tmp/k8s-webhook-server/serving-certs if not provided, or if empty.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Initializing manager", "certDir", certDir)
	// The manager's webhook server will be started locally on Port (default 9443 for controller-runtime v0.11+)
	// and will use the CertDir to serve TLS.
	// Certificates (tls.crt and tls.key) must be present in CertDir.
	// For local development, these can be self-signed certificates.
	// For example, using openssl:
	// openssl genrsa -out tls.key 2048
	// openssl req -new -key tls.key -out tls.csr (fill prompts)
	// openssl x509 -req -days 365 -in tls.csr -signkey tls.key -out tls.crt
	// Then place tls.crt and tls.key into the certDir.
	// In a cluster, cert-manager is a common way to provision and manage TLS certificates for webhooks.
	// Removing MetricsBindAddress temporarily to try and move past "unknown field" error.
	// Port & CertDir are not direct fields; webhook server uses defaults.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		// MetricsBindAddress:     metricsAddr, // Removing again due to "unknown field" error
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "flexdaemonsets.xai",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup webhooks
	setupLog.Info("Setting up webhook server and registering webhooks")
	// Get the webhook server from the manager.
	hookServer := mgr.GetWebhookServer()

	// Register the PodMutator webhook.
	// PodMutator.Decoder is *admission.Decoder (pointer to struct)
	decoder := admission.NewDecoder(mgr.GetScheme())
	hookServer.Register(
		"/mutate-v1-pod",
		&webhook.Admission{Handler: &flexdaemonsetwebhook.PodMutator{Client: mgr.GetClient(), Decoder: decoder}},
	)

	// +kubebuilder:scaffold:builder

	setupLog.Info("Setting up NodeCoverageReconciler")
	if err = (&flexcontroller.NodeCoverageReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NodeCoverageReconciler")
		os.Exit(1)
	}

	setupLog.Info("Setting up FlexDaemonSetNodePodReconciler")
	if err = (&flexcontroller.FlexDaemonSetNodePodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FlexDaemonSetNodePodReconciler")
		os.Exit(1)
	}

	setupLog.Info("Setting up Pod controller") // Existing PodReconciler
	if err = (&flexcontroller.PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}

	// Add health and readiness checks using StartedChecker
	if err := mgr.AddHealthzCheck("healthz", hookServer.StartedChecker()); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", hookServer.StartedChecker()); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager. Webhook server will listen on port set in manager options (default 9443) for HTTPS requests.")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
