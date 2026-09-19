package kubernetes

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/builder"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcpconfig"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/mcptopology"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"gopkg.in/yaml.v3"
)

type Inventory struct {
	ClusterID            string
	ClusterName          string
	Workloads            []Workload
	Services             []Service
	ConfigMaps           map[string]ConfigMap
	ConfigMapErrors      int
	ConfigMapDenied      int
	ConfigMapParseErrors int
	ResourceErrors       []ResourceError
	CRDs                 []CRD
}
type ResourceError struct {
	Resource string
	Denied   bool
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
	Backends  []string
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
	TargetID       string
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
	if options.TargetID == "" {
		options.TargetID = discovery.StableID(options.OrganizationID, discovery.KindCluster, options.Inventory.ClusterID)
	}
	if options.SourceID == "" {
		options.SourceID = options.TargetID
	}
	snapshot := discovery.NewTargetSnapshot(options.OrganizationID, options.SourceID, options.TargetID, discovery.SourceKubernetes, discovery.Collector{ID: "barrikade-lens-k8s", Name: "Barrikade Lens Kubernetes", Version: Version, Mode: "controller"})
	snapshot.Full = options.Full
	snapshot.Sequence = options.Sequence
	snapshot.Scope = discovery.Scope{Name: options.Inventory.ClusterName}
	b := builder.New(snapshot)
	configMapParseErrors := options.Inventory.ConfigMapParseErrors
	clusterEvidence := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.cluster", DetectorVersion: Version, Method: "workload_uid", Family: "cluster_identity", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, options.Inventory.ClusterID), Authoritative: true})
	clusterID := b.AddEntity(discovery.KindCluster, "cluster:"+options.Inventory.ClusterID, options.Inventory.ClusterName, map[string]any{"connected": true, "source_surface": "kubernetes"}, clusterEvidence)
	workloadIDs := map[string]string{}
	for _, workload := range options.Inventory.Workloads {
		ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.workload", DetectorVersion: Version, Method: "workload_uid", Family: "deployment", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, workload.UID), Authoritative: true})
		attrs := map[string]any{"namespace": workload.Namespace, "workload_kind": workload.Kind, "running_at_scan": workload.Running, "images": sorted(workload.Images), "image_digests": imageDigests(workload.Images), "environment_keys": sorted(workload.EnvironmentKeys), "mount_names": sorted(workload.MountNames), "configmap_references": sorted(workload.ConfigMapRefs)}
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
		linkWorkloadRepository(b, options, workload, id, ref)
		for _, credentialRef := range sorted(workload.CredentialRefs) {
			credentialID := b.AddEntity(discovery.KindCredentialReference, "kubernetes:"+options.Inventory.ClusterID+":credential-ref:"+workload.Namespace+":"+credentialRef, workload.Namespace+"/"+credentialRef, map[string]any{"configured": true, "reference_type": "kubernetes_secret"}, ref)
			b.AddRelationship(discovery.RelationshipConfiguredBy, id, credentialID, nil, ref)
		}
		matched := matchRuntimes(options.Pack, workload)
		detectionRefs := []string{}
		refsByRuntime := map[string][]string{}
		for _, detection := range matched {
			for _, observation := range detection.Observations {
				detectionRef := b.AddEvidence(builder.Observation{DetectorID: detection.Signature.ID, DetectorVersion: options.Pack.Version, Method: observation.Method, Family: observation.Family, Specificity: observation.Specificity, Locator: discovery.HashLocator(options.OrganizationID, workload.UID+":"+detection.Signature.ID+":"+observation.Family)})
				detectionRefs = append(detectionRefs, detectionRef)
				refsByRuntime[detection.Signature.ID] = append(refsByRuntime[detection.Signature.ID], detectionRef)
			}
		}
		intent, hasAgentIntent := agentIntentObservation(workload)
		if hasAgentIntent {
			intentRef := b.AddEvidence(builder.Observation{
				DetectorID: "kubernetes.agent-intent", DetectorVersion: Version, Method: intent.Method,
				Family: intent.Family, Specificity: intent.Specificity,
				Locator:       discovery.HashLocator(options.OrganizationID, workload.UID+":agent-intent"),
				Authoritative: intent.Authoritative,
			})
			detectionRefs = append(detectionRefs, intentRef)
		}
		for _, detection := range matched {
			runtimeRefs := refsByRuntime[detection.Signature.ID]
			runtimeID := b.AddEntity(discovery.KindRuntime, "runtime:"+detection.Signature.ID, detection.Signature.Name, map[string]any{"product_id": detection.Signature.ID, "product_category": detection.Signature.Category, "source_surface": "kubernetes"}, runtimeRefs...)
			b.AddRelationship(discovery.RelationshipUses, id, runtimeID, map[string]any{"running_at_scan": workload.Running, "source_surface": "kubernetes"}, runtimeRefs...)
		}
		configurationOwnerID := id
		if hasAgentIntent {
			agentID := b.AddEntity(discovery.KindAgent, "kubernetes:"+options.Inventory.ClusterID+":agent:"+workload.UID, workload.Name, map[string]any{"deployed": true, "running_at_scan": workload.Running, "namespace": workload.Namespace, "source_surface": "kubernetes"}, detectionRefs...)
			b.AddRelationship(discovery.RelationshipDeployedAs, agentID, id, nil, detectionRefs...)
			for _, detection := range matched {
				runtimeRefs := refsByRuntime[detection.Signature.ID]
				runtimeID := b.AddEntity(discovery.KindRuntime, "runtime:"+detection.Signature.ID, detection.Signature.Name, nil, runtimeRefs...)
				b.AddRelationship(discovery.RelationshipUses, agentID, runtimeID, map[string]any{"running_at_scan": workload.Running, "source_surface": "kubernetes"}, runtimeRefs...)
			}
			configurationOwnerID = agentID
		}
		configMapParseErrors += scanReferencedConfigMaps(b, options, workload, configurationOwnerID, ref)
	}
	serviceIDs := map[string]string{}
	serviceRefs := map[string]string{}
	for _, service := range options.Inventory.Services {
		ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.service", DetectorVersion: Version, Method: "workload_uid", Family: "network_service", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, service.UID), Authoritative: true})
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
		serviceKey := service.Namespace + "/" + service.Kind + "/" + service.Name
		serviceIDs[serviceKey] = apiID
		serviceRefs[serviceKey] = ref
		for _, workload := range options.Inventory.Workloads {
			if workload.Namespace == service.Namespace && selectorMatches(service.Selector, workload.Labels) {
				if workloadID := workloadIDs[workload.Namespace+"/"+workload.Name]; workloadID != "" {
					b.AddRelationship(discovery.RelationshipExposes, workloadID, apiID, nil, ref)
				}
			}
		}
	}
	for _, ingress := range options.Inventory.Services {
		if ingress.Kind != "Ingress" {
			continue
		}
		ingressID := serviceIDs[ingress.Namespace+"/Ingress/"+ingress.Name]
		ingressRef := serviceRefs[ingress.Namespace+"/Ingress/"+ingress.Name]
		for _, backend := range ingress.Backends {
			serviceKey := ingress.Namespace + "/Service/" + backend
			if serviceID := serviceIDs[serviceKey]; serviceID != "" {
				b.AddRelationship(discovery.RelationshipExposes, serviceID, ingressID, map[string]any{"routing_basis": "ingress_backend"}, serviceRefs[serviceKey], ingressRef)
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
		ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.crd", DetectorVersion: Version, Method: "descriptor", Family: "custom_resource_definition", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, crd.UID), Authoritative: true})
		id := b.AddEntity(discovery.KindFramework, "kubernetes-crd:"+crd.Group+":"+crd.Kind, crd.Kind, map[string]any{"crd_name": crd.Name, "api_group": crd.Group, "defined_in_cluster": true}, ref)
		b.AddRelationship(discovery.RelationshipProvides, clusterID, id, nil, ref)
	}
	b.Snapshot.Coverage.DetectorsRun = 3 + len(options.Pack.Runtimes)
	b.Snapshot.Coverage.LocationsChecked = len(options.Inventory.Workloads) + len(options.Inventory.Services)
	if options.Inventory.ConfigMapErrors > 0 {
		b.Snapshot.Coverage.Partial = true
		b.Snapshot.Coverage.DetectorsFailed += options.Inventory.ConfigMapErrors
		b.Snapshot.Coverage.LocationsDenied += options.Inventory.ConfigMapDenied
		b.Snapshot.Errors = append(b.Snapshot.Errors, discovery.ScanError{DetectorID: "kubernetes.configmap", Code: "referenced_configmap_unavailable", Message: "One or more referenced ConfigMaps could not be inspected"})
	}
	if configMapParseErrors > 0 {
		b.Snapshot.Coverage.Partial = true
		b.Snapshot.Coverage.DetectorsFailed += configMapParseErrors
		b.Snapshot.Errors = append(b.Snapshot.Errors, discovery.ScanError{DetectorID: "kubernetes.configmap", Code: "referenced_configmap_parse_failed", Message: "One or more referenced MCP ConfigMap entries could not be parsed"})
	}
	for _, resourceError := range options.Inventory.ResourceErrors {
		b.Snapshot.Coverage.Partial = true
		b.Snapshot.Coverage.DetectorsFailed++
		if resourceError.Denied {
			b.Snapshot.Coverage.LocationsDenied++
		}
		code := "kubernetes_resource_unavailable"
		if resourceError.Denied {
			code = "kubernetes_resource_denied"
		}
		b.Snapshot.Errors = append(b.Snapshot.Errors, discovery.ScanError{DetectorID: "kubernetes." + resourceError.Resource, Code: code, Message: "A Kubernetes resource surface could not be inspected", Retryable: !resourceError.Denied})
	}
	return b.Finish()
}

func imageDigests(images []string) []string {
	result := []string{}
	for _, image := range images {
		if index := strings.LastIndex(image, "@sha256:"); index >= 0 {
			digest := strings.ToLower(image[index+1:])
			if len(digest) == len("sha256:")+64 {
				valid := true
				for _, character := range strings.TrimPrefix(digest, "sha256:") {
					if !strings.ContainsRune("0123456789abcdef", character) {
						valid = false
						break
					}
				}
				if valid {
					result = append(result, digest)
				}
			}
		}
	}
	return sorted(result)
}

func linkWorkloadRepository(b *builder.Builder, options Options, workload Workload, workloadID, ref string) {
	repositoryURL := ""
	for _, key := range []string{"barrikade.ai/repository-url", "org.opencontainers.image.source"} {
		if value := discovery.NormalizeRepositoryURL(workload.Labels[key]); value != "" {
			repositoryURL = value
			break
		}
	}
	if repositoryURL == "" {
		return
	}
	attributes := map[string]any{"repository_url": repositoryURL, "source_surface": "kubernetes"}
	if revision := strings.TrimSpace(workload.Labels["org.opencontainers.image.revision"]); len(revision) == 40 || len(revision) == 64 {
		attributes["commit_sha"] = strings.ToLower(revision)
	}
	repositoryID := b.AddEntity(discovery.KindRepository, repositoryURL, repositoryURL, attributes, ref)
	b.AddRelationship(discovery.RelationshipDefinedIn, workloadID, repositoryID, map[string]any{"correlation_basis": "explicit_repository_label"}, ref)
}

type runtimeObservation struct {
	Method        string
	Family        string
	Specificity   string
	Authoritative bool
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
				if imageMatches(image, expected) {
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
		if len(observations) == 0 && containsIdentifierToken(haystack, signature.ID) {
			add("name_signature", "name", "medium")
		}
		if len(observations) > 0 {
			result = append(result, runtimeDetection{Signature: signature, Observations: observations})
		}
	}
	return result
}

func imageMatches(image, expected string) bool {
	image = strings.ToLower(strings.TrimSpace(image))
	expected = strings.ToLower(strings.TrimSpace(expected))
	if image == "" || expected == "" {
		return false
	}
	if at := strings.IndexByte(image, '@'); at >= 0 {
		image = image[:at]
	}
	lastSlash := strings.LastIndexByte(image, '/')
	lastColon := strings.LastIndexByte(image, ':')
	if lastColon > lastSlash {
		image = image[:lastColon]
	}
	if strings.Contains(expected, "/") {
		return image == expected || strings.HasSuffix(image, "/"+expected)
	}
	repository := image
	if index := strings.LastIndexByte(repository, '/'); index >= 0 {
		repository = repository[index+1:]
	}
	return repository == expected
}

func containsIdentifierToken(value, expected string) bool {
	value = strings.ToLower(value)
	expected = strings.ToLower(strings.TrimSpace(expected))
	if len(expected) < 2 {
		return false
	}
	for start := 0; start < len(value); {
		index := strings.Index(value[start:], expected)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || !isIdentifierCharacter(value[index-1])
		after := index + len(expected)
		afterOK := after == len(value) || !isIdentifierCharacter(value[after])
		if beforeOK && afterOK {
			return true
		}
		start = index + 1
	}
	return false
}

func isIdentifierCharacter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func agentIntentObservation(workload Workload) (runtimeObservation, bool) {
	for key, value := range workload.Labels {
		normalizedKey := strings.ToLower(key)
		normalizedValue := strings.ToLower(strings.TrimSpace(value))
		if (containsIdentifierToken(normalizedKey, "agent") || containsIdentifierToken(normalizedKey, "a2a")) && normalizedValue != "false" && normalizedValue != "disabled" {
			return runtimeObservation{Method: "label_signature", Family: "agent_identity", Specificity: "high", Authoritative: true}, true
		}
		if normalizedKey == "app.kubernetes.io/component" && (normalizedValue == "agent" || normalizedValue == "a2a-agent") {
			return runtimeObservation{Method: "label_signature", Family: "agent_identity", Specificity: "high", Authoritative: true}, true
		}
	}
	for _, value := range append(append([]string{workload.Name}, workload.Images...), workload.Commands...) {
		for _, token := range []string{"agent", "a2a"} {
			if containsIdentifierToken(value, token) {
				return runtimeObservation{Method: "name_signature", Family: "agent_identity", Specificity: "medium"}, true
			}
		}
	}
	return runtimeObservation{}, false
}

func scanReferencedConfigMaps(b *builder.Builder, options Options, workload Workload, ownerID, workloadRef string) int {
	parseErrors := 0
	for _, name := range workload.ConfigMapRefs {
		config, ok := options.Inventory.ConfigMaps[workload.Namespace+"/"+name]
		if !ok {
			continue
		}
		for key, value := range config.Data {
			if len(value) > 4<<20 {
				if strings.Contains(strings.ToLower(key), "mcp") {
					parseErrors++
				}
				continue
			}
			document := map[string]any{}
			if json.Unmarshal([]byte(value), &document) != nil {
				if err := yaml.Unmarshal([]byte(value), &document); err != nil {
					if strings.Contains(strings.ToLower(key), "mcp") {
						parseErrors++
					}
					continue
				}
			}
			servers := mcpconfig.Find(document)
			if len(servers) == 0 {
				continue
			}
			ref := b.AddEvidence(builder.Observation{DetectorID: "kubernetes.configmap", DetectorVersion: Version, Method: "descriptor", Family: "mcp_configuration", Specificity: "high", Locator: discovery.HashLocator(options.OrganizationID, config.Namespace+"/"+config.Name+"/"+key), ContentHash: discovery.ContentHash([]byte(value)), Authoritative: true})
			for _, server := range servers {
				mcptopology.Add(b, mcptopology.Context{
					LocalCanonicalPrefix: "kubernetes:" + options.Inventory.ClusterID + ":" + config.Namespace,
					SourceSurface:        discovery.SourceKubernetes,
					Source:               "configmap",
				}, ownerID, server, workloadRef, ref)
			}
		}
	}
	return parseErrors
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
