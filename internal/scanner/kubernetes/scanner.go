package kubernetes

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"gopkg.in/yaml.v3"
)

type Inventory struct {
	ClusterID       string
	ClusterName     string
	Workloads       []Workload
	Services        []Service
	ConfigMaps      map[string]ConfigMap
	ConfigMapErrors int
	CRDs            []CRD
}
type Workload struct {
	UID             string
	Namespace       string
	Kind            string
	Name            string
	Labels          map[string]string
	Images          []string
	Commands        []string
	EnvironmentKeys []string
	ConfigMapRefs   []string
	CredentialRefs  []string
	MountNames      []string
	Running         bool
}
type Service struct {
	UID       string
	Namespace string
	Kind      string
	Name      string
	Hosts     []string
	Ports     []int
	Selector  map[string]string
}
type ConfigMap struct {
	Namespace string
	Name      string
	Data      map[string]string
}
type CRD struct {
	UID    string
	Name   string
	Group  string
	Kind   string
	Labels map[string]string
}

type Options struct {
	OrganizationID string
	SourceID       string
	Full           bool
	Sequence       uint64
	Pack           detector.Pack
	Inventory      Inventory
}

func Scan(options Options) (discovery.Snapshot, error) {
	if options.OrganizationID == "" {
		options.OrganizationID = "local"
	}
	if options.Pack.ID == "" {
		var err error
		options.Pack, err = detector.Builtin()
		if err != nil {
			return discovery.Snapshot{}, err
		}
	}
	if options.Inventory.ClusterID == "" {
		return discovery.Snapshot{}, fmt.Errorf("cluster ID is required")
	}
	if options.SourceID == "" {
		options.SourceID = discovery.StableID(options.OrganizationID, discovery.KindCluster, options.Inventory.ClusterID)
	}
	snapshot := discovery.NewSnapshot(options.OrganizationID, options.SourceID, discovery.SourceKubernetes, discovery.Collector{ID: "barrikade-lens-k8s", Name: "Barrikade Lens Kubernetes", Version: Version, Mode: "controller"})
	snapshot.Full = options.Full
	snapshot.Sequence = options.Sequence
	snapshot.Scope = discovery.Scope{Name: options.Inventory.ClusterName}
	b := builder.New(snapshot)
	clusterEvidence := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.cluster", DetectorVersion: Version, Method: "workload_uid", Family: "cluster_identity", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, options.Inventory.ClusterID)})
	clusterID := b.AddEntity(discovery.KindCluster, "cluster:"+options.Inventory.ClusterID, options.Inventory.ClusterName, map[string]any{"connected": true}, clusterEvidence)
	workloadIDs := map[string]string{}
	for _, workload := range options.Inventory.Workloads {
		ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.workload", DetectorVersion: Version, Method: "workload_uid", Family: "deployment", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, workload.UID)})
		attrs := map[string]any{"namespace": workload.Namespace, "workload_kind": workload.Kind, "running_at_scan": workload.Running, "images": sorted(workload.Images), "environment_keys": sorted(workload.EnvironmentKeys), "mount_names": sorted(workload.MountNames), "configmap_references": sorted(workload.ConfigMapRefs)}
		if len(workload.CredentialRefs) > 0 {
			attrs["credential_reference_count"] = len(sorted(workload.CredentialRefs))
		}
		commands := []string{}
		for _, command := range workload.Commands {
			fields := strings.Fields(command)
			if len(fields) == 0 {
				continue
			}
			if name := filepath.Base(fields[0]); name != "" {
				commands = append(commands, name)
			}
		}
		if len(commands) > 0 {
			attrs["command_names"] = sorted(commands)
		}
		id := b.AddEntity(discovery.KindWorkload, "kubernetes:"+options.Inventory.ClusterID+":"+workload.UID, workload.Namespace+"/"+workload.Name, attrs, ref)
		workloadIDs[workload.Namespace+"/"+workload.Name] = id
		b.AddRelationship(discovery.RelationshipRunsOn, id, clusterID, nil, ref)
		for _, credentialRef := range sorted(workload.CredentialRefs) {
			credentialID := b.AddEntity(discovery.KindCredentialReference, "kubernetes:"+options.Inventory.ClusterID+":credential-ref:"+workload.Namespace+":"+credentialRef, workload.Namespace+"/"+credentialRef, map[string]any{"configured": true, "reference_type": "kubernetes_secret"}, ref)
			b.AddRelationship(discovery.RelationshipConfiguredBy, id, credentialID, nil, ref)
		}
		matched := matchRuntimes(options.Pack, workload)
		if len(matched) > 0 {
			detectionRefs := []string{}
			refsByRuntime := map[string][]string{}
			for _, detection := range matched {
				for _, observation := range detection.Observations {
					detectionRef := b.AddEvidence(builder.Observation{DetectorID: detection.Signature.ID, DetectorVersion: options.Pack.Version, Method: observation.Method, Family: observation.Family, Specificity: observation.Specificity, Locator: discovery.HashLocator(options.OrganizationID, workload.UID+":"+detection.Signature.ID+":"+observation.Family)})
					detectionRefs = append(detectionRefs, detectionRef)
					refsByRuntime[detection.Signature.ID] = append(refsByRuntime[detection.Signature.ID], detectionRef)
				}
			}
			agentID := b.AddEntity(discovery.KindAgent, "kubernetes:"+options.Inventory.ClusterID+":agent:"+workload.UID, workload.Name, map[string]any{"deployed": true, "namespace": workload.Namespace}, detectionRefs...)
			b.AddRelationship(discovery.RelationshipDeployedAs, agentID, id, nil, detectionRefs...)
			for _, detection := range matched {
				runtimeRefs := refsByRuntime[detection.Signature.ID]
				runtimeID := b.AddEntity(discovery.KindRuntime, "runtime:"+detection.Signature.ID, detection.Signature.Name, map[string]any{"running_at_scan": workload.Running}, runtimeRefs...)
				b.AddRelationship(discovery.RelationshipUses, agentID, runtimeID, nil, runtimeRefs...)
			}
			scanReferencedConfigMaps(b, options, workload, agentID, ref)
		}
	}
	for _, service := range options.Inventory.Services {
		ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.service", DetectorVersion: Version, Method: "workload_uid", Family: "network_service", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, service.UID)})
		attributes := map[string]any{"namespace": service.Namespace, "service_kind": service.Kind, "ports": service.Ports}
		hosts := []string{}
		for _, host := range service.Hosts {
			host = strings.ToLower(strings.TrimSpace(host))
			if host != "" && !discovery.IsCloudMetadataHost(host) {
				hosts = append(hosts, host)
			}
		}
		if len(hosts) > 0 {
			attributes["hosts"] = sorted(hosts)
			attributes["host"] = hosts[0]
		}
		apiID := b.AddEntity(discovery.KindAPIService, "kubernetes:"+options.Inventory.ClusterID+":service:"+service.UID, service.Namespace+"/"+service.Name, attributes, ref)
		for _, workload := range options.Inventory.Workloads {
			if workload.Namespace == service.Namespace && selectorMatches(service.Selector, workload.Labels) {
				if workloadID := workloadIDs[workload.Namespace+"/"+workload.Name]; workloadID != "" {
					b.AddRelationship(discovery.RelationshipExposes, workloadID, apiID, nil, ref)
				}
			}
		}
	}
	for _, crd := range options.Inventory.CRDs {
		haystack := strings.ToLower(crd.Name + " " + crd.Group + " " + crd.Kind)
		for key, value := range crd.Labels {
			haystack += " " + strings.ToLower(key+" "+value)
		}
		if !strings.Contains(haystack, "agent") && !strings.Contains(haystack, "mcp") && !strings.Contains(haystack, "a2a") {
			continue
		}
		ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.crd", DetectorVersion: Version, Method: "descriptor", Family: "custom_resource_definition", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, crd.UID)})
		id := b.AddEntity(discovery.KindFramework, "kubernetes-crd:"+crd.Group+":"+crd.Kind, crd.Kind, map[string]any{"crd_name": crd.Name, "api_group": crd.Group, "defined_in_cluster": true}, ref)
		b.AddRelationship(discovery.RelationshipProvides, clusterID, id, nil, ref)
	}
	b.Snapshot.Coverage.DetectorsRun = 3 + len(options.Pack.Runtimes)
	b.Snapshot.Coverage.LocationsChecked = len(options.Inventory.Workloads) + len(options.Inventory.Services)
	if options.Inventory.ConfigMapErrors > 0 {
		b.Snapshot.Coverage.Partial = true
		b.Snapshot.Coverage.LocationsDenied += options.Inventory.ConfigMapErrors
		b.Snapshot.Errors = append(b.Snapshot.Errors, discovery.ScanError{DetectorID: "kubernetes.configmap", Code: "referenced_configmap_unavailable", Message: "One or more referenced ConfigMaps could not be inspected"})
	}
	return b.Finish()
}

type runtimeObservation struct {
	Method      string
	Family      string
	Specificity string
}

type runtimeDetection struct {
	Signature    detector.RuntimeSignature
	Observations []runtimeObservation
}

func matchRuntimes(pack detector.Pack, workload Workload) []runtimeDetection {
	haystack := strings.ToLower(strings.Join(append(append([]string{}, workload.Images...), workload.Commands...), " "))
	for key, value := range workload.Labels {
		haystack += " " + strings.ToLower(key+" "+value)
	}
	result := []runtimeDetection{}
	for _, signature := range pack.Runtimes {
		observations := []runtimeObservation{}
		families := map[string]bool{}
		add := func(method, family, specificity string) {
			if !families[family] {
				families[family] = true
				observations = append(observations, runtimeObservation{Method: method, Family: family, Specificity: specificity})
			}
		}
		for _, image := range workload.Images {
			for _, expected := range signature.Images {
				if strings.Contains(strings.ToLower(image), strings.ToLower(expected)) {
					add("image_signature", "container_image", "high")
				}
			}
		}
		for _, command := range workload.Commands {
			fields := strings.Fields(command)
			if len(fields) == 0 {
				continue
			}
			name := strings.ToLower(filepath.Base(fields[0]))
			for _, process := range signature.Processes {
				if name == strings.ToLower(process) {
					add("process_name", "container_command", "high")
				}
			}
		}
		for _, key := range workload.EnvironmentKeys {
			for _, expected := range signature.EnvironmentKeys {
				if strings.EqualFold(key, expected) {
					add("environment_key_name", "environment_key", "high")
				}
			}
		}
		if len(observations) == 0 && strings.Contains(haystack, strings.ToLower(signature.ID)) {
			add("name_signature", "name", "medium")
		}
		if len(observations) > 0 {
			result = append(result, runtimeDetection{Signature: signature, Observations: observations})
		}
	}
	return result
}

func scanReferencedConfigMaps(b *builder.Builder, options Options, workload Workload, agentID, workloadRef string) {
	for _, name := range workload.ConfigMapRefs {
		config, ok := options.Inventory.ConfigMaps[workload.Namespace+"/"+name]
		if !ok {
			continue
		}
		for key, value := range config.Data {
			if len(value) > 4<<20 {
				continue
			}
			document := map[string]any{}
			if json.Unmarshal([]byte(value), &document) != nil {
				_ = yaml.Unmarshal([]byte(value), &document)
			}
			servers := findMCPServers(document)
			if len(servers) == 0 {
				continue
			}
			ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.configmap", DetectorVersion: Version, Method: "descriptor", Family: "mcp_configuration", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, config.Namespace+"/"+config.Name+"/"+key), ContentHash: discovery.ContentHash([]byte(value))})
			for serverName, raw := range servers {
				serverConfig, _ := raw.(map[string]any)
				attrs := map[string]any{"configured": true, "source": "configmap"}
				canonical := "kubernetes:" + options.Inventory.ClusterID + ":mcp:" + config.Namespace + ":" + serverName
				if rawURL, ok := serverConfig["url"].(string); ok {
					sanitized, err := discovery.SanitizeURL(rawURL)
					if err != nil {
						continue
					}
					attrs["endpoint"] = sanitized
					attrs["host"] = discovery.URLHost(sanitized)
					attrs["transport"] = "http"
				} else {
					attrs["transport"] = "stdio"
				}
				serverID := b.AddEntity(discovery.KindMCPServer, canonical, serverName, attrs, ref)
				b.AddRelationship(discovery.RelationshipConnectsTo, agentID, serverID, nil, workloadRef, ref)
			}
		}
	}
}

func findMCPServers(value any) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for key, child := range object {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
		if normalized == "mcpservers" {
			result, _ := child.(map[string]any)
			return result
		}
		if nested := findMCPServers(child); nested != nil {
			return nested
		}
	}
	return nil
}
func selectorMatches(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}
func sorted(values []string) []string {
	unique := map[string]bool{}
	for _, value := range values {
		if value != "" {
			unique[value] = true
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

var Version = "2.0.0-dev"
