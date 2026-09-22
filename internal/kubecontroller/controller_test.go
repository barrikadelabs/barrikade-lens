package kubecontroller

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	scanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/kubernetes"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestOnlyReferencedConfigMapBodiesAreFetched(t *testing.T) {
	client := fake.NewClientset(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "used"}, Data: map[string]string{"mcp.json": "{}"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: "unused"}, Data: map[string]string{"private": "must not be read"}},
	)
	inventory := scanner.Inventory{ConfigMaps: map[string]scanner.ConfigMap{}, Workloads: []scanner.Workload{{Namespace: "agents", ConfigMapRefs: []string{"used"}}}}
	populateReferencedConfigMaps(context.Background(), client, &inventory)
	if len(inventory.ConfigMaps) != 1 || inventory.ConfigMaps["agents/used"].Name != "used" {
		t.Fatalf("unexpected ConfigMap inventory: %#v", inventory.ConfigMaps)
	}
	actions := client.Actions()
	if len(actions) != 1 || actions[0].GetVerb() != "get" || actions[0].GetResource().Resource != "configmaps" {
		t.Fatalf("controller performed unexpected ConfigMap operations: %#v", actions)
	}
}

func TestDeniedReferencedConfigMapIsReportedWithoutReadingSecrets(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("get", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, &forbiddenError{}
	})
	inventory := scanner.Inventory{ConfigMaps: map[string]scanner.ConfigMap{}, Workloads: []scanner.Workload{{Namespace: "agents", ConfigMapRefs: []string{"used"}}}}
	populateReferencedConfigMaps(context.Background(), client, &inventory)
	if inventory.ConfigMapErrors != 1 || inventory.ConfigMapDenied != 1 {
		t.Fatalf("denial was not preserved as coverage: %#v", inventory)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "secrets" || action.GetSubresource() == "exec" {
			t.Fatalf("controller attempted forbidden secret/exec action: %#v", action)
		}
	}
}

type forbiddenError struct{}

func (*forbiddenError) Error() string { return "forbidden" }
func (*forbiddenError) Status() metav1.Status {
	return metav1.Status{Reason: metav1.StatusReasonForbidden, Code: 403}
}

func TestControllerRetriesHubOutageAndFullReconciliationKeepsClusterIdentity(t *testing.T) {
	state := filepath.Join(t.TempDir(), "config.json")
	if err := lensconfig.Save(state, lensconfig.Config{ConfigVersion: 2, OrganizationID: "org", SourceID: "source", TargetID: "cluster-target"}); err != nil {
		t.Fatal(err)
	}
	client := fake.NewClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{UID: "workload-uid", Namespace: "agents", Name: "worker"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan discovery.Snapshot, 4)
	attempts := 0
	controller := &Controller{
		Client: client, ConfigPath: state, ClusterID: "cluster-uid", ClusterName: "cluster", Version: "test",
		ResyncInterval: 100 * time.Millisecond, RetryBaseDelay: time.Millisecond,
		UploadSnapshot: func(_ context.Context, _ *lensconfig.Config, snapshot discovery.Snapshot) error {
			attempts++
			if attempts == 1 {
				return errors.New("temporary Hub outage")
			}
			result <- snapshot
			return nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	first, second := discovery.Snapshot{}, discovery.Snapshot{}
	for index := 0; index < 2; index++ {
		select {
		case snapshot := <-result:
			if index == 0 {
				first = snapshot
			} else {
				second = snapshot
			}
		case err := <-done:
			t.Fatalf("controller stopped before full reconciliation: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("controller did not retry and reconcile")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller did not stop")
	}
	if attempts < 3 || !first.Full || !second.Full || first.TargetID != second.TargetID || first.TargetID != "cluster-target" || second.Sequence <= first.Sequence {
		t.Fatalf("recovery lost scan state: attempts=%d first=%#v second=%#v", attempts, first, second)
	}
	cfg, err := lensconfig.Load(state)
	if err != nil || cfg.Sequence != second.Sequence {
		t.Fatalf("reconciled sequence was not persisted: %#v %v", cfg, err)
	}
	restartContext, stopRestart := context.WithCancel(context.Background())
	restarted := *controller
	restartResult := make(chan discovery.Snapshot, 1)
	restarted.UploadSnapshot = func(_ context.Context, _ *lensconfig.Config, snapshot discovery.Snapshot) error {
		restartResult <- snapshot
		stopRestart()
		return nil
	}
	if err := restarted.Run(restartContext); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case afterRestart := <-restartResult:
		if afterRestart.TargetID != first.TargetID || afterRestart.Sequence <= second.Sequence || !afterRestart.Full {
			t.Fatalf("restart changed cluster identity or lost full reconciliation: %#v", afterRestart)
		}
	default:
		t.Fatal("restart did not upload a full reconciliation")
	}
}
