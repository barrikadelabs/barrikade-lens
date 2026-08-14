package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var tabNames = []string{"Overview", "Systems", "Capabilities", "Coverage", "Evidence graph"}

const (
	brandOrange = "#FF6B00"
	brandGreen  = "#39DF76"
	brandBlue   = "#5AB8F7"
	brandAmber  = "#FFB546"
	brandMuted  = "#777777"
)

type Model struct {
	snapshot discovery.Snapshot
	tab      int
	offset   int
	width    int
	height   int
	noColor  bool
}

type systemSummary struct {
	entity      discovery.Entity
	systemType  string
	state       string
	network     string
	connections int
	user        string
	owner       bool
}

type attentionItem struct {
	count  int
	title  string
	detail string
}

func New(snapshot discovery.Snapshot) Model {
	return Model{snapshot: snapshot, width: 80, height: 24, noColor: os.Getenv("NO_COLOR") != ""}
}

func Run(snapshot discovery.Snapshot, input io.Reader, output io.Writer) error {
	_, err := tea.NewProgram(New(snapshot), tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen()).Run()
	return err
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = typed.Width, typed.Height
	case tea.KeyMsg:
		switch typed.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "right", "tab", "l":
			m.tab = (m.tab + 1) % len(tabNames)
			m.offset = 0
		case "left", "shift+tab", "h":
			m.tab = (m.tab - 1 + len(tabNames)) % len(tabNames)
			m.offset = 0
		case "1", "2", "3", "4", "5":
			m.tab = int(typed.Runes[0] - '1')
			m.offset = 0
		case "down", "j":
			m.offset++
		case "up", "k":
			if m.offset > 0 {
				m.offset--
			}
		case "pgdown":
			m.offset += max(1, m.height-8)
		case "pgup":
			m.offset = max(0, m.offset-max(1, m.height-8))
		case "home", "g":
			m.offset = 0
		case "end", "G":
			m.offset = 1 << 20
		}
	}
	return m, nil
}

func (m Model) View() string {
	width := max(30, m.width)
	lines := m.header(width)
	lines = append(lines, "", m.renderTabs(width), divider(m.noColor, width))
	content := m.content(width)
	visible := max(1, m.height-len(lines)-2)
	maxOffset := max(0, len(content)-visible)
	offset := min(m.offset, maxOffset)
	end := min(len(content), offset+visible)
	if offset < len(content) {
		lines = append(lines, content[offset:end]...)
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	footer := "1–5 views  ←/→ switch  ↑/↓ scroll  q quit"
	if maxOffset > 0 {
		footer += fmt.Sprintf("  ·  %d–%d/%d", offset+1, end, len(content))
	}
	lines = append(lines, muted(m.noColor, trimWidth(footer, width)))
	return strings.Join(lines, "\n")
}

func (m Model) header(width int) []string {
	brand := "■  BARRIKADE  /  LENS"
	boundary := "DISCOVER"
	if !m.noColor {
		brand = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brandOrange)).Render("■  BARRIKADE") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Render("  /  LENS")
		boundary = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brandOrange)).Render(boundary)
	}
	line := brand
	if width >= 54 {
		line += strings.Repeat(" ", max(1, width-lipgloss.Width(brand)-lipgloss.Width(boundary))) + boundary
	}
	scanLabel := completedLabel(m.snapshot)
	if width < 100 {
		scanLabel = "scan complete"
		if m.snapshot.Coverage.Partial {
			scanLabel = "partial scan"
		}
	}
	surface := m.surfaceLabel(width)
	meta := fmt.Sprintf("%s  ·  %s  ·  %s", nonEmpty(m.snapshot.Scope.Name, "local target"), surface, scanLabel)
	return []string{line, muted(m.noColor, trimWidth(meta, width))}
}

func (m Model) surfaceLabel(width int) string {
	surfaces := map[string]bool{string(m.snapshot.SourceType): true}
	for _, entity := range m.snapshot.Entities {
		if surface := stringAttr(entity.Attributes, "source_surface"); surface != "" {
			surfaces[surface] = true
		}
		switch entity.Kind {
		case discovery.KindRepository:
			surfaces["repository"] = true
		case discovery.KindCluster:
			surfaces["kubernetes"] = true
		}
	}
	labels := []string{}
	for _, surface := range []string{"endpoint", "repository", "kubernetes"} {
		if surfaces[surface] {
			label := surface
			if width < 70 && surface == "repository" {
				label = "repo"
			}
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return pretty(string(m.snapshot.SourceType))
	}
	return strings.Join(labels, " + ")
}

func (m Model) renderTabs(width int) string {
	if width < 86 {
		return accent(m.noColor, fmt.Sprintf("%d/%d  %s", m.tab+1, len(tabNames), strings.ToUpper(tabNames[m.tab])))
	}
	parts := make([]string, 0, len(tabNames))
	for index, name := range tabNames {
		label := fmt.Sprintf("%d %s", index+1, name)
		if index == m.tab {
			label = "[" + label + "]"
			label = accent(m.noColor, label)
		} else {
			label = muted(m.noColor, label)
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "   ")
}

func (m Model) content(width int) []string {
	switch m.tab {
	case 0:
		return m.overview(width)
	case 1:
		return m.systems(width)
	case 2:
		return m.capabilities(width)
	case 3:
		return m.coverage(width)
	default:
		return m.evidence(width)
	}
}

func (m Model) overview(width int) []string {
	systems := m.rootSystems()
	typeCounts, stateCounts := map[string]int{}, map[string]int{}
	for _, item := range systems {
		typeCounts[item.systemType]++
		stateCounts[item.state]++
	}
	capabilityCount := m.countKinds(discovery.KindMCPServer, discovery.KindSkill, discovery.KindModel, discovery.KindModelServer, discovery.KindAPIService, discovery.KindAPIOperation, discovery.KindTool, discovery.KindWorkflow)
	scanStatus := "COMPLETE"
	if m.snapshot.Coverage.Partial {
		scanStatus = "PARTIAL"
	}

	lines := []string{section(m.noColor, "Discovery posture")}
	lines = append(lines, wrap(metricLine(width,
		metric{"ROOT SYSTEMS", len(systems)},
		metric{"RUNNING NOW", stateCounts["running"]},
		metric{"CAPABILITIES", capabilityCount},
		metric{"SCAN", scanStatus}), width)...)
	lines = append(lines, mutedWrap(m.noColor, "Root systems exclude host applications, development runtimes, and cached artifacts.", width)...)
	lines = append(lines, "", section(m.noColor, "System footprint"))
	lines = append(lines, wrap(fmt.Sprintf("%s autonomous agents   %s agent-capable tools   %s model runtimes",
		count(m.noColor, typeCounts["autonomous agent"]), count(m.noColor, typeCounts["agent tool"]), count(m.noColor, typeCounts["model runtime"])), width)...)
	lines = append(lines, wrap(stateDistribution(stateCounts), width)...)
	lines = append(lines, wrap(m.capabilityDistribution(), width)...)

	attention := m.attention(systems)
	lines = append(lines, "", section(m.noColor, "Factual attention"))
	if len(attention) == 0 {
		lines = append(lines, status(m.noColor, brandGreen, "✓ No discovery conditions need review in this snapshot."))
	} else {
		lines = append(lines, mutedWrap(m.noColor, "Conditions can overlap; counts are factual signals, not a risk score.", width)...)
		for _, item := range attention {
			lines = append(lines, wrap(fmt.Sprintf("! %d  %s — %s", item.count, item.title, item.detail), width)...)
		}
	}

	hostApps := m.runtimeCategoryCount("host_application")
	devRuntimes := m.runtimeCategoryCount("development_runtime")
	lines = append(lines, "", section(m.noColor, "Context for interpretation"))
	lines = append(lines, wrap(fmt.Sprintf("Supporting software: %d host applications and %d development runtimes. These are visible under Systems but do not inflate the root-system total.", hostApps, devRuntimes), width)...)
	lines = append(lines, wrap("Confidence describes evidence strength, not security posture. Lens reports discovery facts only—no risk score, approval, or remediation decision.", width)...)
	return lines
}

func (m Model) systems(width int) []string {
	root := m.rootSystems()
	lines := []string{section(m.noColor, fmt.Sprintf("Root systems  %d", len(root)))}
	lines = append(lines, mutedWrap(m.noColor, "Sorted by current state and evidence strength. Possible and residual findings are not confirmed installations.", width)...)
	if len(root) == 0 {
		lines = append(lines, "", "No autonomous agents, agent tools, or model runtimes were established in this scan.")
	}
	groups := []struct {
		name string
		kind string
	}{{"Autonomous agents", "autonomous agent"}, {"Agent-capable tools", "agent tool"}, {"Model runtimes", "model runtime"}}
	for _, group := range groups {
		items := filterSystems(root, group.kind)
		if len(items) == 0 {
			continue
		}
		lines = append(lines, "", subsection(m.noColor, fmt.Sprintf("%s  %d", group.name, len(items))))
		for _, item := range items {
			lines = append(lines, m.systemLines(item, width)...)
		}
	}

	supporting := m.supportingRuntimes()
	lines = append(lines, "", section(m.noColor, fmt.Sprintf("Supporting software  %d", len(supporting))))
	if len(supporting) == 0 {
		lines = append(lines, mutedWrap(m.noColor, "No host applications or development runtimes discovered.", width)...)
	} else {
		lines = append(lines, mutedWrap(m.noColor, "Context for agent operation; excluded from executive system counts.", width)...)
		for _, item := range supporting {
			lines = append(lines, m.systemLines(item, width)...)
		}
	}
	return lines
}

func (m Model) systemLines(item systemSummary, width int) []string {
	marker := stateMarker(item.state)
	line := fmt.Sprintf("%s %s  [%s]", marker, item.entity.Name, strings.ToUpper(item.state))
	if item.network != "none" && item.network != "unknown" {
		line += "  ·  " + pretty(item.network)
	}
	lines := wrap(line, width)
	if !m.noColor && len(lines) > 0 {
		lines[0] = strings.Replace(lines[0], marker, status(false, stateColor(item.state), marker), 1)
	}
	details := []string{pretty(item.systemType), string(item.entity.Confidence)}
	details = append(details, presenceFacts(item.entity.Attributes)...)
	if item.connections > 0 {
		details = append(details, fmt.Sprintf("%d linked capabilities", item.connections))
	}
	if item.user != "" {
		label := "observed user"
		if item.owner {
			label = "owner"
		}
		details = append(details, label+" "+item.user)
	}
	lines = append(lines, wrap("  "+strings.Join(unique(details), "  ·  "), width)...)
	return lines
}

func (m Model) capabilities(width int) []string {
	groups := []struct {
		name  string
		kinds []discovery.EntityKind
	}{
		{"MCP servers", []discovery.EntityKind{discovery.KindMCPServer}},
		{"Models and model services", []discovery.EntityKind{discovery.KindModel, discovery.KindModelServer}},
		{"Skills and tools", []discovery.EntityKind{discovery.KindSkill, discovery.KindTool}},
		{"APIs and workflows", []discovery.EntityKind{discovery.KindAPIService, discovery.KindWorkflow}},
	}
	lines := []string{section(m.noColor, "Capability inventory")}
	lines = append(lines, mutedWrap(m.noColor, "Capabilities are grouped beneath the systems that use or provide them where a relationship was established.", width)...)
	any := false
	for _, group := range groups {
		entities := m.entitiesOfKinds(group.kinds...)
		operationCount := 0
		if group.name == "APIs and workflows" {
			operationCount = m.countKinds(discovery.KindAPIOperation)
		}
		if len(entities) == 0 && operationCount == 0 {
			continue
		}
		any = true
		heading := fmt.Sprintf("%s  %d", group.name, len(entities))
		if group.name == "APIs and workflows" {
			heading = fmt.Sprintf("APIs and workflows  %d services  ·  %d operations  ·  %d workflows", m.countKinds(discovery.KindAPIService), operationCount, m.countKinds(discovery.KindWorkflow))
		}
		lines = append(lines, "", subsection(m.noColor, heading))
		if group.name == "APIs and workflows" && operationCount > 0 {
			lines = append(lines, mutedWrap(m.noColor, "Operations are summarized beneath API services; use the Evidence graph or JSON export for the complete operation inventory.", width)...)
		}
		for _, entity := range entities {
			state := m.entityState(entity)
			marker := stateMarker(state)
			itemLabel := fmt.Sprintf("%s %s  [%s]", marker, entity.Name, strings.ToUpper(state))
			if entity.Kind == discovery.KindModel {
				itemLabel += "  ·  " + string(entity.Confidence)
			}
			itemLines := wrap(itemLabel, width)
			if !m.noColor && len(itemLines) > 0 {
				itemLines[0] = strings.Replace(itemLines[0], marker, status(false, stateColor(state), marker), 1)
			}
			lines = append(lines, itemLines...)
			if entity.Kind == discovery.KindModel {
				continue
			}
			facts := []string{pretty(string(entity.Kind)), string(entity.Confidence)}
			facts = append(facts, capabilityFacts(entity.Attributes)...)
			connected := m.connectedNames(entity.ID)
			if len(connected) > 0 {
				facts = append(facts, "linked to "+strings.Join(connected, ", "))
			}
			lines = append(lines, wrap("  "+strings.Join(unique(facts), "  ·  "), width)...)
		}
	}
	if !any {
		lines = append(lines, "")
		lines = append(lines, wrap("No MCP servers, models, skills, tools, APIs, or workflows were discovered.", width)...)
	}
	return lines
}

func (m Model) coverage(width int) []string {
	statusText := "Complete"
	statusColor := brandGreen
	if m.snapshot.Coverage.Partial {
		statusText = "Partial"
		statusColor = brandAmber
	}
	confidence := map[discovery.Confidence]int{}
	for _, entity := range m.snapshot.Entities {
		confidence[entity.Confidence]++
	}
	lines := []string{
		section(m.noColor, "Collection coverage"),
		fmt.Sprintf("Scan status            %s", status(m.noColor, statusColor, statusText)),
		fmt.Sprintf("Discovery surfaces     %s", m.surfaceLabel(width)),
		fmt.Sprintf("Detectors              %d run  ·  %d failed", m.snapshot.Coverage.DetectorsRun, m.snapshot.Coverage.DetectorsFailed),
		fmt.Sprintf("Locations              %d checked  ·  %d unavailable", m.snapshot.Coverage.LocationsChecked, m.snapshot.Coverage.LocationsDenied),
		fmt.Sprintf("Snapshot inventory     %d entities  ·  %d relationships", len(m.snapshot.Entities), len(m.snapshot.Relationships)),
		"",
		section(m.noColor, "Evidence confidence"),
		fmt.Sprintf("Confirmed              %d", confidence[discovery.ConfidenceConfirmed]),
		fmt.Sprintf("Likely                 %d", confidence[discovery.ConfidenceLikely]),
		fmt.Sprintf("Possible               %d", confidence[discovery.ConfidencePossible]),
	}
	lines = append(lines, mutedWrap(m.noColor, "Confirmed requires an authoritative descriptor or independent high-specificity evidence families.", width)...)
	if len(m.snapshot.Coverage.Notes) > 0 || len(m.snapshot.Errors) > 0 {
		lines = append(lines, "", section(m.noColor, "Collection gaps"))
		for _, note := range m.snapshot.Coverage.Notes {
			lines = append(lines, wrap("! "+note, width)...)
		}
		for _, item := range m.snapshot.Errors {
			lines = append(lines, wrap(fmt.Sprintf("! %s  [%s]", item.Message, item.Code), width)...)
		}
	} else {
		lines = append(lines, "", section(m.noColor, "Collection gaps"), status(m.noColor, brandGreen, "✓ No detector failures or denied locations reported."))
	}
	lines = append(lines, "", section(m.noColor, "Privacy boundary"))
	lines = append(lines, wrap("This local scan made no network calls. Lens keeps sanitized locators and content hashes; config bodies, environment values, prompts, credentials, and full command arguments are excluded.", width)...)
	return lines
}

func (m Model) evidence(width int) []string {
	methods, families := map[string]int{}, map[string]int{}
	for _, item := range m.snapshot.Evidence {
		methods[pretty(item.Method)]++
		families[pretty(item.Family)]++
	}
	lines := []string{section(m.noColor, fmt.Sprintf("Evidence quality  %d observations", len(m.snapshot.Evidence)))}
	lines = append(lines, compactCounts(m.noColor, "Methods", methods, width)...)
	lines = append(lines, compactCounts(m.noColor, "Families", families, width)...)

	lines = append(lines, "", section(m.noColor, "Evidence findings"))
	if len(m.snapshot.Evidence) == 0 {
		lines = append(lines, "No evidence observations were recorded.")
	} else {
		subjects := evidenceSubjects(m.snapshot)
		limit := min(10, len(m.snapshot.Evidence))
		for _, item := range m.snapshot.Evidence[:limit] {
			subject := strings.Join(subjects[item.ID], ", ")
			if subject == "" {
				subject = pretty(strings.TrimPrefix(item.DetectorID, "runtime."))
			}
			lines = append(lines, wrap(fmt.Sprintf("• %s  —  %s", tuiEvidenceTitle(item.Method, item.Family), subject), width)...)
			lines = append(lines, mutedWrap(m.noColor, fmt.Sprintf("  %s  ·  %s  ·  %s", tuiEvidenceLocation(item.Locator), tuiEvidenceReason(item.Method, item.Family), tuiEvidenceNextStep(item.Method, item.Family, subject)), width)...)
		}
		if len(m.snapshot.Evidence) > limit {
			lines = append(lines, mutedWrap(m.noColor, fmt.Sprintf("  %d more observations available in the JSON export", len(m.snapshot.Evidence)-limit), width)...)
		}
	}

	lines = append(lines, "", section(m.noColor, fmt.Sprintf("Evidence graph  %d connections", len(m.snapshot.Relationships))))
	names := m.entityNames()
	if len(m.snapshot.Relationships) == 0 {
		lines = append(lines, "No cross-entity relationships were established.")
	} else {
		for _, relation := range m.snapshot.Relationships {
			from, to := nonEmpty(names[relation.From], shortID(relation.From)), nonEmpty(names[relation.To], shortID(relation.To))
			lines = append(lines, wrap(fmt.Sprintf("• %s  %s  %s  ·  %s", from, relationshipPhrase(relation.Kind), to, relation.Confidence), width)...)
		}
	}
	lines = append(lines, "", section(m.noColor, "Export"))
	lines = append(lines, wrap("barrikade-lens scan --format json|ndjson|cyclonedx --output <file>", width)...)
	lines = append(lines, mutedWrap(m.noColor, "Lens JSON is the canonical evidence graph; exports contain no credential values or config bodies.", width)...)
	return lines
}

func evidenceSubjects(snapshot discovery.Snapshot) map[string][]string {
	result := map[string][]string{}
	names := map[string]string{}
	for _, entity := range snapshot.Entities {
		names[entity.ID] = entity.Name
		for _, evidenceID := range entity.EvidenceRefs {
			result[evidenceID] = appendUnique(result[evidenceID], entity.Name)
		}
	}
	for _, relationship := range snapshot.Relationships {
		subject := nonEmpty(names[relationship.From], shortID(relationship.From)) + " → " + nonEmpty(names[relationship.To], shortID(relationship.To))
		for _, evidenceID := range relationship.EvidenceRefs {
			result[evidenceID] = appendUnique(result[evidenceID], subject)
		}
	}
	return result
}

func appendUnique(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func tuiEvidenceTitle(method, family string) string {
	if family == "skill" && method != "skill_descriptor" {
		return "Unvalidated skill directory signal"
	}
	titles := map[string]string{
		"application": "Application installation found", "package": "Package installation found",
		"extension_manifest": "IDE extension manifest matched", "executable": "Executable available",
		"process": "Process running at scan time", "listener": "Listening service observed",
		"config_shape": "Configuration structure matched", "config_file": "Configuration file present",
		"skill_descriptor": "SKILL.md descriptor validated", "descriptor": "Authoritative descriptor found",
		"agent_descriptor": "Agent descriptor validated", "import": "Framework import found",
	}
	if title := titles[method]; title != "" {
		return title
	}
	return pretty(family) + " evidence found"
}

func tuiEvidenceLocation(locator string) string {
	value := strings.TrimSpace(locator)
	lower := strings.ToLower(value)
	if value == "" {
		return "Location not retained"
	}
	if strings.HasPrefix(lower, "sha256:") || strings.HasPrefix(lower, "path_hash:") {
		return "Protected endpoint location"
	}
	if strings.HasPrefix(lower, "tcp-listener:") {
		return "TCP port " + strings.TrimPrefix(value, "tcp-listener:")
	}
	return value
}

func tuiEvidenceReason(method, family string) string {
	reasons := map[string]string{
		"config_shape": "known configuration fields matched", "config_file": "known configuration location exists",
		"application": "product application path matched", "package": "recognized package matched",
		"extension_manifest": "publisher and extension ID matched", "executable": "recognized executable found",
		"process": "recognized live process found", "listener": "listener and process signal matched",
		"skill_descriptor": "SKILL.md name matched its directory and required metadata was present", "agent_descriptor": "valid agent descriptor parsed",
		"descriptor": "authoritative descriptor parsed",
	}
	if reason := reasons[method]; reason != "" {
		return reason
	}
	if family == "skill" {
		return "path existed under a configured skill root; no valid SKILL.md was associated"
	}
	return "known " + strings.ToLower(pretty(family)) + " signal matched"
}

func tuiEvidenceNextStep(method, family, subject string) string {
	switch method {
	case "process":
		return "confirm the running process and responsible owner"
	case "listener":
		return "review its binding and owning process"
	case "config_shape", "config_file":
		return "review the local configuration and declared capabilities"
	case "application", "package", "extension_manifest", "executable":
		return "confirm the installation and who uses it"
	case "descriptor", "skill_descriptor", "agent_descriptor":
		return "review the descriptor, its instructions, capabilities, and owner"
	default:
		if family == "skill" {
			return "confirm whether it contains a valid SKILL.md before treating it as a skill"
		}
		return "confirm whether " + subject + " is expected"
	}
}

func (m Model) rootSystems() []systemSummary {
	result := []systemSummary{}
	for _, entity := range m.snapshot.Entities {
		systemType := ""
		switch entity.Kind {
		case discovery.KindAgent:
			systemType = "autonomous agent"
		case discovery.KindRuntime:
			switch stringAttr(entity.Attributes, "product_category") {
			case "agent_tool":
				systemType = "agent tool"
			case "model_runtime":
				systemType = "model runtime"
			}
		}
		if systemType == "" {
			continue
		}
		user, owner := m.attribution(entity.ID)
		result = append(result, systemSummary{entity: entity, systemType: systemType, state: m.entityState(entity), network: m.entityNetwork(entity), connections: m.capabilityConnections(entity.ID), user: user, owner: owner})
	}
	sortSystems(result)
	return result
}

func (m Model) supportingRuntimes() []systemSummary {
	result := []systemSummary{}
	for _, entity := range m.snapshot.Entities {
		if entity.Kind != discovery.KindRuntime {
			continue
		}
		category := stringAttr(entity.Attributes, "product_category")
		label := ""
		switch category {
		case "host_application":
			label = "host application"
		case "development_runtime":
			label = "development runtime"
		case "unclassified", "":
			label = "unclassified runtime"
		}
		if label == "" {
			continue
		}
		user, owner := m.attribution(entity.ID)
		result = append(result, systemSummary{entity: entity, systemType: label, state: m.entityState(entity), network: m.entityNetwork(entity), connections: m.capabilityConnections(entity.ID), user: user, owner: owner})
	}
	sortSystems(result)
	return result
}

func (m Model) attention(systems []systemSummary) []attentionItem {
	items := []attentionItem{}
	if m.snapshot.Coverage.Partial {
		items = append(items, attentionItem{1, "Partial scan", "some configured discovery surfaces were not fully checked"})
	}
	failures := m.snapshot.Coverage.DetectorsFailed + len(m.snapshot.Errors)
	if failures > 0 {
		items = append(items, attentionItem{failures, "Collection errors", "review Coverage before relying on absence"})
	}
	network := 0
	for _, entity := range m.snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer || entity.Kind == discovery.KindModelServer || entity.Kind == discovery.KindAPIService {
			scope := m.entityNetwork(entity)
			if scope == "network" || scope == "external" {
				network++
			}
		}
	}
	if network > 0 {
		items = append(items, attentionItem{network, "Non-loopback services", "listeners were observed beyond the local loopback interface"})
	}
	possible, residual := 0, 0
	for _, item := range systems {
		if item.entity.Confidence == discovery.ConfidencePossible {
			possible++
		}
		if item.state == "residual" {
			residual++
		}
	}
	if possible > 0 {
		items = append(items, attentionItem{possible, "Possible-only systems", "evidence needs corroboration before treating these as present"})
	}
	if residual > 0 {
		items = append(items, attentionItem{residual, "Residual system state", "state directories exist without a stronger installed or configured signal"})
	}
	disabled := 0
	for _, entity := range m.snapshot.Entities {
		if entity.Kind == discovery.KindMCPServer && boolAttr(entity.Attributes, "configured") && entity.Attributes["enabled"] == false {
			disabled++
		}
	}
	if disabled > 0 {
		items = append(items, attentionItem{disabled, "Disabled MCP configurations", "configuration exists but is explicitly disabled"})
	}
	return items
}

func (m Model) entityState(entity discovery.Entity) string {
	for _, pair := range []struct{ key, state string }{
		{"running_at_scan", "running"}, {"deployed", "deployed"}, {"defined", "defined"}, {"configured", "configured"}, {"installed", "installed"}, {"state_present", "residual"}, {"cached", "cached"},
	} {
		if boolAttr(entity.Attributes, pair.key) {
			return pair.state
		}
		for _, relation := range m.snapshot.Relationships {
			if (relation.From == entity.ID || relation.To == entity.ID) && boolAttr(relation.Attributes, pair.key) {
				return pair.state
			}
		}
	}
	return "observed"
}

func (m Model) entityNetwork(entity discovery.Entity) string {
	return networkFromAttributes(entity.Attributes)
}

func networkFromAttributes(attrs map[string]any) string {
	if boolAttr(attrs, "external") {
		return "external"
	}
	switch stringAttr(attrs, "network_scope") {
	case "loopback", "network", "external", "none", "unknown":
		return stringAttr(attrs, "network_scope")
	}
	switch stringAttr(attrs, "binding") {
	case "loopback":
		return "loopback"
	case "interface", "all_interfaces", "network":
		return "network"
	}
	if boolAttr(attrs, "running_at_scan") && stringAttr(attrs, "transport") != "stdio" {
		return "unknown"
	}
	return "none"
}

func (m Model) capabilityConnections(id string) int {
	capabilities := map[string]bool{}
	kinds := map[string]discovery.EntityKind{}
	for _, entity := range m.snapshot.Entities {
		kinds[entity.ID] = entity.Kind
	}
	for _, relation := range m.snapshot.Relationships {
		other := ""
		if relation.From == id {
			other = relation.To
		} else if relation.To == id {
			other = relation.From
		}
		switch kinds[other] {
		case discovery.KindMCPServer, discovery.KindSkill, discovery.KindModel, discovery.KindModelServer, discovery.KindTool, discovery.KindAPIService, discovery.KindAPIOperation, discovery.KindWorkflow:
			capabilities[other] = true
		}
	}
	return len(capabilities)
}

func (m Model) attribution(id string) (string, bool) {
	names := m.entityNames()
	for _, relation := range m.snapshot.Relationships {
		if relation.Kind != discovery.RelationshipOwnedBy || relation.From != id {
			continue
		}
		return names[relation.To], boolAttr(relation.Attributes, "authoritative")
	}
	return "", false
}

func (m Model) connectedNames(id string) []string {
	names, kinds := m.entityNames(), map[string]discovery.EntityKind{}
	for _, entity := range m.snapshot.Entities {
		kinds[entity.ID] = entity.Kind
	}
	result := []string{}
	for _, relation := range m.snapshot.Relationships {
		other := ""
		if relation.From == id {
			other = relation.To
		} else if relation.To == id {
			other = relation.From
		}
		if other == "" || kinds[other] == discovery.KindEndpoint || kinds[other] == discovery.KindUser {
			continue
		}
		result = append(result, names[other])
	}
	result = unique(result)
	sort.Strings(result)
	if len(result) > 3 {
		result = append(result[:3], fmt.Sprintf("+%d more", len(result)-3))
	}
	return result
}

func (m Model) entityNames() map[string]string {
	result := map[string]string{}
	for _, entity := range m.snapshot.Entities {
		result[entity.ID] = entity.Name
	}
	return result
}

func (m Model) entitiesOfKinds(kinds ...discovery.EntityKind) []discovery.Entity {
	allowed := map[discovery.EntityKind]bool{}
	for _, kind := range kinds {
		allowed[kind] = true
	}
	result := []discovery.Entity{}
	for _, entity := range m.snapshot.Entities {
		if allowed[entity.Kind] {
			result = append(result, entity)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind == result[j].Kind {
			return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
		}
		return result[i].Kind < result[j].Kind
	})
	return result
}

func (m Model) countKinds(kinds ...discovery.EntityKind) int { return len(m.entitiesOfKinds(kinds...)) }

func (m Model) runtimeCategoryCount(category string) int {
	count := 0
	for _, entity := range m.snapshot.Entities {
		if entity.Kind == discovery.KindRuntime && stringAttr(entity.Attributes, "product_category") == category {
			count++
		}
	}
	return count
}

func (m Model) capabilityDistribution() string {
	parts := []string{}
	for _, item := range []struct {
		singular string
		plural   string
		kinds    []discovery.EntityKind
	}{
		{"MCP server", "MCP servers", []discovery.EntityKind{discovery.KindMCPServer}},
		{"model", "models", []discovery.EntityKind{discovery.KindModel}},
		{"model service", "model services", []discovery.EntityKind{discovery.KindModelServer}},
		{"skill", "skills", []discovery.EntityKind{discovery.KindSkill}},
		{"tool", "tools", []discovery.EntityKind{discovery.KindTool}},
		{"API service", "API services", []discovery.EntityKind{discovery.KindAPIService}},
		{"API operation", "API operations", []discovery.EntityKind{discovery.KindAPIOperation}},
		{"workflow", "workflows", []discovery.EntityKind{discovery.KindWorkflow}},
	} {
		value := m.countKinds(item.kinds...)
		if value > 0 {
			label := item.plural
			if value == 1 {
				label = item.singular
			}
			parts = append(parts, fmt.Sprintf("%d %s", value, label))
		}
	}
	if len(parts) == 0 {
		return "Capabilities: none established."
	}
	return "Capabilities: " + strings.Join(parts, "  ·  ")
}

type metric struct {
	label string
	value any
}

func metricLine(width int, metrics ...metric) string {
	parts := make([]string, 0, len(metrics))
	for _, item := range metrics {
		parts = append(parts, fmt.Sprintf("%s %v", item.label, item.value))
	}
	separator := "   │   "
	if width < 76 {
		separator = "  ·  "
	}
	return strings.Join(parts, separator)
}

func compactCounts(noColor bool, title string, values map[string]int, width int) []string {
	lines := []string{"", subsection(noColor, title)}
	parts := []string{}
	for _, pair := range sortedCounts(values) {
		parts = append(parts, fmt.Sprintf("%s %d", pair.name, pair.count))
	}
	if len(parts) == 0 {
		return append(lines, muted(noColor, "None recorded"))
	}
	return append(lines, wrap(strings.Join(parts, "  ·  "), width)...)
}

type countPair struct {
	name  string
	count int
}

func sortedCounts(values map[string]int) []countPair {
	result := make([]countPair, 0, len(values))
	for name, count := range values {
		result = append(result, countPair{name, count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].count == result[j].count {
			return result[i].name < result[j].name
		}
		return result[i].count > result[j].count
	})
	return result
}

func sortSystems(items []systemSummary) {
	stateRank := map[string]int{"running": 0, "deployed": 1, "defined": 2, "configured": 3, "installed": 4, "residual": 5, "cached": 6, "observed": 7}
	confidenceRank := map[discovery.Confidence]int{discovery.ConfidenceConfirmed: 0, discovery.ConfidenceLikely: 1, discovery.ConfidencePossible: 2}
	sort.Slice(items, func(i, j int) bool {
		if stateRank[items[i].state] != stateRank[items[j].state] {
			return stateRank[items[i].state] < stateRank[items[j].state]
		}
		if confidenceRank[items[i].entity.Confidence] != confidenceRank[items[j].entity.Confidence] {
			return confidenceRank[items[i].entity.Confidence] < confidenceRank[items[j].entity.Confidence]
		}
		return strings.ToLower(items[i].entity.Name) < strings.ToLower(items[j].entity.Name)
	})
}

func filterSystems(items []systemSummary, kind string) []systemSummary {
	result := []systemSummary{}
	for _, item := range items {
		if item.systemType == kind {
			result = append(result, item)
		}
	}
	return result
}

func stateDistribution(values map[string]int) string {
	parts := []string{}
	for _, state := range []string{"running", "deployed", "defined", "configured", "installed", "residual", "cached", "observed"} {
		if values[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", values[state], state))
		}
	}
	if len(parts) == 0 {
		return "No root-system state to summarize."
	}
	return "Primary state: " + strings.Join(parts, "  ·  ")
}

func presenceFacts(attrs map[string]any) []string {
	result := []string{}
	strong := false
	for _, pair := range []struct{ key, label string }{{"installed", "installed"}, {"configured", "configured"}, {"running_at_scan", "running at scan"}, {"deployed", "deployed"}} {
		if boolAttr(attrs, pair.key) {
			result = append(result, pair.label)
			strong = true
		}
	}
	if !strong && boolAttr(attrs, "state_present") {
		result = append(result, "residual state only")
	}
	return result
}

func capabilityFacts(attrs map[string]any) []string {
	result := presenceFacts(attrs)
	for _, key := range []string{"transport", "binding", "host", "port", "provider", "type"} {
		if value, ok := attrs[key]; ok && fmt.Sprint(value) != "" {
			result = append(result, pretty(key)+" "+pretty(fmt.Sprint(value)))
		}
	}
	if enabled, ok := attrs["enabled"].(bool); ok {
		if enabled {
			result = append(result, "enabled")
		} else {
			result = append(result, "disabled")
		}
	}
	return result
}

func relationshipPhrase(kind discovery.RelationshipKind) string {
	switch kind {
	case discovery.RelationshipRunsOn:
		return "runs on"
	case discovery.RelationshipDefinedIn:
		return "is defined in"
	case discovery.RelationshipDeployedAs:
		return "is deployed as"
	case discovery.RelationshipUses:
		return "uses"
	case discovery.RelationshipExposes:
		return "exposes"
	case discovery.RelationshipConnectsTo:
		return "connects to"
	case discovery.RelationshipProvides:
		return "provides"
	case discovery.RelationshipInvokes:
		return "invokes"
	case discovery.RelationshipConfiguredBy:
		return "is configured by"
	case discovery.RelationshipOwnedBy:
		return "was observed for user"
	default:
		return pretty(string(kind))
	}
}

func completedLabel(snapshot discovery.Snapshot) string {
	label := "scan complete"
	if snapshot.Coverage.Partial {
		label = "partial scan"
	}
	when, err := time.Parse(time.RFC3339Nano, snapshot.ObservedAt)
	if err == nil {
		label += " " + when.Local().Format("15:04 MST")
	}
	return label
}

func stateMarker(state string) string {
	switch state {
	case "running":
		return "●"
	case "deployed", "defined", "configured", "installed":
		return "◆"
	case "residual", "possible":
		return "?"
	case "cached":
		return "◇"
	default:
		return "·"
	}
}

func stateColor(state string) string {
	switch state {
	case "running":
		return brandGreen
	case "deployed", "defined", "configured", "installed":
		return brandBlue
	case "residual":
		return brandAmber
	default:
		return brandMuted
	}
}

func section(noColor bool, text string) string {
	text = strings.ToUpper(text)
	if noColor {
		return text
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Render(text)
}

func subsection(noColor bool, text string) string {
	if noColor {
		return text
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brandOrange)).Render(text)
}

func accent(noColor bool, text string) string {
	if noColor {
		return text
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(brandOrange)).Render(text)
}

func muted(noColor bool, text string) string {
	if noColor {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(brandMuted)).Render(text)
}

func mutedWrap(noColor bool, text string, width int) []string {
	lines := wrap(text, width)
	for index := range lines {
		lines[index] = muted(noColor, lines[index])
	}
	return lines
}

func status(noColor bool, color, text string) string {
	if noColor {
		return text
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Render(text)
}

func count(_ bool, value int) string { return fmt.Sprint(value) }

func divider(noColor bool, width int) string {
	line := strings.Repeat("─", max(1, width))
	if noColor {
		return line
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#2A2A2A")).Render(line)
}

func wrap(text string, width int) []string {
	width = max(20, width-1)
	if utf8.RuneCountInString(text) <= width {
		return []string{text}
	}
	indent := text[:len(text)-len(strings.TrimLeft(text, " "))]
	words := strings.Fields(text)
	lines, current := []string{}, ""
	for _, word := range words {
		for utf8.RuneCountInString(word) > width {
			if current != "" {
				lines = append(lines, current)
				current = indent
			}
			runes := []rune(word)
			lines = append(lines, string(runes[:width]))
			word = string(runes[width:])
		}
		candidate := word
		if current != "" {
			candidate = current + " " + word
		} else if indent != "" {
			candidate = indent + word
		}
		if utf8.RuneCountInString(candidate) > width {
			lines = append(lines, current)
			current = indent + word
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func trimWidth(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	plain := []rune(text)
	if width <= 1 {
		return string(plain[:max(0, width)])
	}
	for len(plain) > 0 && lipgloss.Width(string(plain))+1 > width {
		plain = plain[:len(plain)-1]
	}
	return string(plain) + "…"
}

func boolAttr(attrs map[string]any, key string) bool {
	value, _ := attrs[key].(bool)
	return value
}

func stringAttr(attrs map[string]any, key string) string {
	value, _ := attrs[key].(string)
	return value
}

func pretty(value string) string {
	value = strings.ReplaceAll(value, "_", " ")
	if value == "" {
		return "unknown"
	}
	return value
}

func unique(values []string) []string {
	seen, result := map[string]bool{}, []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func shortID(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:13] + "…"
}
