package kubecontroller

import (
	"os"
	"strings"
	"testing"
)

func TestHelmChartPersistsIdentityAndUsesSingleWriterUpgrade(t *testing.T) {
	deployment, err := os.ReadFile("../../deploy/helm/lens-k8s/templates/deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pvc, err := os.ReadFile("../../deploy/helm/lens-k8s/templates/pvc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(deployment)
	for _, required := range []string{
		"strategy: {type: Recreate}",
		"/var/lib/lens/config.json",
		"name: collector-state",
		"persistentVolumeClaim",
		"readOnlyRootFilesystem: true",
		"allowPrivilegeEscalation: false",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("Kubernetes deployment is missing %q", required)
		}
	}
	if !strings.Contains(string(pvc), "ReadWriteOnce") {
		t.Fatal("collector state PVC must remain single-writer")
	}
}

func TestHelmEnrollmentRequiresBothOneUseInputs(t *testing.T) {
	manifest, err := os.ReadFile("../../deploy/helm/lens-k8s/templates/enrollment-secret.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(manifest)
	for _, required := range []string{"hubURL is required for enrollment", "enrollmentCode is required for enrollment", "LENS_HUB_URL", "LENS_ENROLLMENT_CODE"} {
		if !strings.Contains(text, required) {
			t.Fatalf("enrollment Secret is missing %q", required)
		}
	}
}
