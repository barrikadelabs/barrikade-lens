package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/internal/hubclient"
	"github.com/barrikadelabs/barrikade-lens/internal/kubecontroller"
	scanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var version = "2.0.0-dev"

func main() {
	if err := run(); err != nil {
		slog.Error("Lens Kubernetes controller stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", os.Getenv("BARRIKADE_LENS_CONFIG"), "collector enrollment configuration")
	bootstrapConfig := flag.String("bootstrap-config", os.Getenv("LENS_BOOTSTRAP_CONFIG"), "read-only enrollment configuration copied into writable state on first start")
	hubURL := flag.String("hub-url", os.Getenv("LENS_HUB_URL"), "Lens Hub URL used for single-use enrollment")
	enrollmentCode := flag.String("enrollment-code", os.Getenv("LENS_ENROLLMENT_CODE"), "single-use Kubernetes enrollment code")
	kubeconfig := flag.String("kubeconfig", os.Getenv("KUBECONFIG"), "kubeconfig path for out-of-cluster use")
	clusterID := flag.String("cluster-id", os.Getenv("LENS_CLUSTER_ID"), "stable cluster identity (defaults to kube-system UID)")
	clusterName := flag.String("cluster-name", os.Getenv("LENS_CLUSTER_NAME"), "display name for this cluster")
	resync := flag.Duration("full-relist", 6*time.Hour, "full reconciliation interval")
	flag.Parse()
	if *configPath == "" {
		var err error
		*configPath, err = config.Path()
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(*configPath); os.IsNotExist(err) && *bootstrapConfig != "" {
		initial, loadErr := config.Load(*bootstrapConfig)
		if loadErr != nil {
			return fmt.Errorf("load bootstrap configuration: %w", loadErr)
		}
		if saveErr := config.Save(*configPath, initial); saveErr != nil {
			return fmt.Errorf("initialize controller state: %w", saveErr)
		}
	}
	var restConfig *rest.Config
	var err error
	if *kubeconfig != "" {
		restConfig, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		restConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return fmt.Errorf("create Kubernetes client configuration: %w", err)
	}
	restConfig.UserAgent = "barrikade-lens-k8s/" + version
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	extensions, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	metadataClient, err := metadata.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *clusterID == "" {
		namespace, discoverErr := client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
		if discoverErr != nil {
			return fmt.Errorf("discover cluster identity: %w", discoverErr)
		}
		*clusterID = string(namespace.UID)
	}
	if *clusterName == "" {
		*clusterName = *clusterID
	}
	if _, statErr := os.Stat(*configPath); os.IsNotExist(statErr) && *enrollmentCode != "" && *hubURL != "" {
		initial, enrollErr := hubclient.New(version).EnrollTarget(ctx, *hubURL, *enrollmentCode, *configPath, "kubernetes", *clusterID, *clusterName)
		if enrollErr != nil {
			return fmt.Errorf("enroll Kubernetes cluster: %w", enrollErr)
		}
		if saveErr := config.Save(*configPath, initial); saveErr != nil {
			return fmt.Errorf("save Kubernetes enrollment: %w", saveErr)
		}
	}
	scanner.Version = version
	return (&kubecontroller.Controller{Client: client, Metadata: metadataClient, Extensions: extensions, ConfigPath: *configPath, ClusterID: *clusterID, ClusterName: *clusterName, Version: version, ResyncInterval: *resync, Logger: slog.Default()}).Run(ctx)
}
