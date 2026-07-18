package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var tabNames = []string{"Coverage", "Agents & runtimes", "MCP, skills & models", "Relationships & APIs", "Evidence & export"}

type Model struct {
	snapshot discovery.Snapshot
	tab      int
	offset   int
	width    int
	height   int
	noColor  bool
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
		case "home":
			m.offset = 0
		}
	}
	return m, nil
}

func (m Model) View() string {
	width := max(30, m.width)
	header := "BARRIKADE LENS  /  DISCOVERY"
	if !m.noColor {
		header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render(header)
	}
	lines := []string{header, muted(m.noColor, fmt.Sprintf("%s · %s · %s", m.snapshot.Scope.Name, m.snapshot.SourceType, freshness(m.snapshot))), ""}
	lines = append(lines, m.renderTabs(width), "")
	content := m.content(width)
	visible := max(1, m.height-len(lines)-2)
	maxOffset := max(0, len(content)-visible)
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	end := min(len(content), m.offset+visible)
	if m.offset < len(content) {
		lines = append(lines, content[m.offset:end]...)
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	footer := "←/→ areas  ↑/↓ scroll  q quit"
	if maxOffset > 0 {
		footer += fmt.Sprintf("  ·  %d–%d/%d", m.offset+1, end, len(content))
	}
	lines = append(lines, muted(m.noColor, footer))
	return strings.Join(lines, "\n")
}

func (m Model) renderTabs(width int) string {
	if width < 100 {
		return fmt.Sprintf("[%d/%d] %s", m.tab+1, len(tabNames), tabNames[m.tab])
	}
	parts := make([]string, 0, len(tabNames))
	for index, name := range tabNames {
		if index == m.tab {
			if m.noColor {
				name = "[" + name + "]"
			} else {
				name = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Render("[" + name + "]")
			}
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, "  ")
}

func (m Model) content(width int) []string {
	switch m.tab {
	case 0:
		return m.coverage(width)
	case 1:
		return m.entities(width, []discovery.EntityKind{discovery.KindAgent, discovery.KindRuntime, discovery.KindFramework, discovery.KindEndpoint, discovery.KindRepository, discovery.KindWorkload})
	case 2:
		return m.entities(width, []discovery.EntityKind{discovery.KindMCPServer, discovery.KindSkill, discovery.KindModel, discovery.KindModelServer, discovery.KindTool})
	case 3:
		return m.relationships(width)
	default:
		return m.evidence(width)
	}
}

func (m Model) coverage(width int) []string {
	status := "Complete"
	if m.snapshot.Coverage.Partial {
		status = "Partial"
	}
	lines := []string{
		section(m.noColor, "Scan coverage"),
		fmt.Sprintf("Status                 %s", status),
		fmt.Sprintf("Detectors run          %d", m.snapshot.Coverage.DetectorsRun),
		fmt.Sprintf("Locations checked      %d", m.snapshot.Coverage.LocationsChecked),
		fmt.Sprintf("Locations unavailable  %d", m.snapshot.Coverage.LocationsDenied),
		fmt.Sprintf("Entities discovered    %d", len(m.snapshot.Entities)),
		fmt.Sprintf("Relationships          %d", len(m.snapshot.Relationships)),
	}
	if len(m.snapshot.Coverage.Notes) > 0 || len(m.snapshot.Errors) > 0 {
		lines = append(lines, "", section(m.noColor, "Coverage notes"))
		for _, note := range m.snapshot.Coverage.Notes {
			lines = append(lines, wrap("• "+note, width)...)
		}
		for _, item := range m.snapshot.Errors {
			lines = append(lines, wrap("• "+item.Message+" ("+item.Code+")", width)...)
		}
	}
	lines = append(lines, "", section(m.noColor, "Privacy boundary"))
	lines = append(lines, wrap("Local scans make no network calls. Lens records evidence hashes and factual presence metadata; configuration bodies, environment values, prompts, credentials, and full command arguments never enter the snapshot.", width)...)
	return lines
}

func (m Model) entities(width int, kinds []discovery.EntityKind) []string {
	allowed := map[discovery.EntityKind]bool{}
	for _, kind := range kinds {
		allowed[kind] = true
	}
	entities := []discovery.Entity{}
	for _, entity := range m.snapshot.Entities {
		if allowed[entity.Kind] {
			entities = append(entities, entity)
		}
	}
	if len(entities) == 0 {
		return []string{"Nothing in this area was discovered during this scan.", "", muted(m.noColor, "This is an inventory result, not a security assessment.")}
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Kind == entities[j].Kind {
			return entities[i].Name < entities[j].Name
		}
		return entities[i].Kind < entities[j].Kind
	})
	lines := []string{section(m.noColor, fmt.Sprintf("Discovered inventory (%d)", len(entities)))}
	for _, entity := range entities {
		facts := []string{string(entity.Confidence)}
		for _, key := range []string{"installed", "configured", "running_at_scan", "enabled", "transport", "host", "os", "type"} {
			if value, ok := entity.Attributes[key]; ok {
				facts = append(facts, fmt.Sprintf("%s=%v", key, value))
			}
		}
		lines = append(lines, wrap(fmt.Sprintf("• %s  [%s]", entity.Name, strings.ReplaceAll(string(entity.Kind), "_", " ")), width)...)
		lines = append(lines, wrap("  "+strings.Join(facts, " · "), width)...)
	}
	return lines
}

func (m Model) relationships(width int) []string {
	name := map[string]string{}
	for _, entity := range m.snapshot.Entities {
		name[entity.ID] = entity.Name
	}
	lines := []string{section(m.noColor, fmt.Sprintf("Evidence graph (%d edges)", len(m.snapshot.Relationships)))}
	if len(m.snapshot.Relationships) == 0 {
		return append(lines, "No relationships were established.")
	}
	for _, relation := range m.snapshot.Relationships {
		lines = append(lines, wrap(fmt.Sprintf("• %s  —%s→  %s  (%s)", name[relation.From], strings.ReplaceAll(string(relation.Kind), "_", " "), name[relation.To], relation.Confidence), width)...)
	}
	return lines
}

func (m Model) evidence(width int) []string {
	methods := map[string]int{}
	families := map[string]int{}
	for _, evidence := range m.snapshot.Evidence {
		methods[evidence.Method]++
		families[evidence.Family]++
	}
	lines := []string{section(m.noColor, fmt.Sprintf("Evidence (%d observations)", len(m.snapshot.Evidence)))}
	for _, pair := range sortedCounts(methods) {
		lines = append(lines, fmt.Sprintf("%-24s %d", pair.name, pair.count))
	}
	lines = append(lines, "", section(m.noColor, "Evidence families"))
	for _, pair := range sortedCounts(families) {
		lines = append(lines, fmt.Sprintf("%-24s %d", pair.name, pair.count))
	}
	lines = append(lines, "", section(m.noColor, "Export this snapshot"))
	lines = append(lines, wrap("Run `barrikade-lens scan --format json|ndjson|cyclonedx --output <file>` for an interoperable export. Lens JSON remains the canonical evidence graph.", width)...)
	return lines
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
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}
func freshness(snapshot discovery.Snapshot) string {
	if snapshot.Coverage.Partial {
		return "partial coverage"
	}
	return "scan complete"
}
func section(noColor bool, text string) string {
	if noColor {
		return strings.ToUpper(text)
	}
	return lipgloss.NewStyle().Bold(true).Render(text)
}
func muted(noColor bool, text string) string {
	if noColor {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(text)
}

func wrap(text string, width int) []string {
	width = max(20, width-2)
	if len([]rune(text)) <= width {
		return []string{text}
	}
	indent := text[:len(text)-len(strings.TrimLeft(text, " "))]
	words := strings.Fields(text)
	lines, current := []string{}, ""
	for _, word := range words {
		for len([]rune(word)) > width {
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
		}
		if len([]rune(candidate)) > width {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
