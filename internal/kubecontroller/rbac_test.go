package kubecontroller

import (
	"os"
	"strings"
	"testing"
)

func TestShippedRBACCannotReadSecretsOrExec(t *testing.T){data,err:=os.ReadFile("../../deploy/helm/lens-k8s/templates/rbac.yaml");if err!=nil{t.Fatal(err)};rbac:=strings.ToLower(string(data));for _,forbidden:=range []string{"\"secrets\"","pods/exec","resources: [\"*\"]"}{if strings.Contains(rbac,forbidden){t.Fatalf("RBAC contains forbidden permission %q",forbidden)}};for _,required:=range []string{"\"pods\"","\"configmaps\"","\"customresourcedefinitions\"","\"get\", \"list\", \"watch\""}{if !strings.Contains(rbac,required){t.Fatalf("RBAC is missing %q",required)}}}
