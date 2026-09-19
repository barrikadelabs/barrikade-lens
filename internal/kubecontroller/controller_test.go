package kubecontroller

import (
	"context"
	"testing"

	scanner "github.com/barrikadelabs/barrikade-lens/internal/scanner/kubernetes"
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
