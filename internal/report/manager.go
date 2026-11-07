// Persistencia del informe (port de ReportManager).
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mirofish/internal/fsx"
	"mirofish/internal/timex"
)

// Manager replica ReportManager.
type Manager struct {
	ReportsDir string

	mu sync.Mutex // serializa escrituras de metadatos y logs
}

// NewManager crea el gestor sobre <uploads>/reports (REPORTS_DIR de Python).
func NewManager(uploadDir string) *Manager {
	return &Manager{ReportsDir: filepath.Join(uploadDir, "reports")}
}

// safeID evita que un id de la URL se convierta en una ruta fuera de reports/.
func safeID(id string) bool {
	return id != "" && id != "." && id != ".." &&
		!strings.ContainsAny(id, `/\`) && !strings.Contains(id, ":")
}

// FolderPath devuelve la carpeta del informe sin crearla.
func (m *Manager) FolderPath(reportID string) string {
	return filepath.Join(m.ReportsDir, reportID)
}

// EnsureFolder crea la carpeta del informe y devuelve su ruta.
func (m *Manager) EnsureFolder(reportID string) (string, error) {
	if !safeID(reportID) {
		return "", fmt.Errorf("report_id inválido: %s", reportID)
	}
	dir := m.FolderPath(reportID)
	return dir, os.MkdirAll(dir, 0o755)
}

// MetaPath, MarkdownPath, OutlinePath, ProgressPath, SectionPath y las rutas de
// log replican los helpers _get_*_path de Python.
func (m *Manager) MetaPath(id string) string { return filepath.Join(m.FolderPath(id), "meta.json") }
func (m *Manager) MarkdownPath(id string) string {
	return filepath.Join(m.FolderPath(id), "full_report.md")
}
func (m *Manager) OutlinePath(id string) string {
	return filepath.Join(m.FolderPath(id), "outline.json")
}
func (m *Manager) ProgressPath(id string) string {
	return filepath.Join(m.FolderPath(id), "progress.json")
}
func (m *Manager) AgentLogPath(id string) string {
	return filepath.Join(m.FolderPath(id), "agent_log.jsonl")
}
func (m *Manager) ConsoleLogPath(id string) string {
	return filepath.Join(m.FolderPath(id), "console_log.txt")
}

// SectionPath devuelve section_XX.md (índice desde 1).
func (m *Manager) SectionPath(id string, index int) string {
	return filepath.Join(m.FolderPath(id), fmt.Sprintf("section_%02d.md", index))
}

// SaveReport replica save_report: meta.json + outline + full_report.md.
func (m *Manager) SaveReport(r *Report) error {
	if _, err := m.EnsureFolder(r.ReportID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	meta, err := json.MarshalIndent(r.ToDict(), "", "  ")
	if err != nil {
		return err
	}
	if err := fsx.WriteFileAtomic(m.MetaPath(r.ReportID), meta, 0o644); err != nil {
		return err
	}
	if r.Outline != nil {
		outline, err := json.MarshalIndent(r.Outline.ToDict(), "", "  ")
		if err != nil {
			return err
		}
		if err := fsx.WriteFileAtomic(m.OutlinePath(r.ReportID), outline, 0o644); err != nil {
			return err
		}
	}
	if r.MarkdownContent != "" {
		if err := fsx.WriteFileAtomic(m.MarkdownPath(r.ReportID), []byte(r.MarkdownContent), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// GetReport replica get_report (con lectura del full_report.md si el meta no
// incluye markdown_content).
func (m *Manager) GetReport(reportID string) *Report {
	if !safeID(reportID) {
		return nil
	}
	raw, err := os.ReadFile(m.MetaPath(reportID))
	if err != nil {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	r := FromDict(data)
	if r.MarkdownContent == "" {
		if md, err := os.ReadFile(m.MarkdownPath(reportID)); err == nil {
			r.MarkdownContent = string(md)
		}
	}
	return r
}

// GetReportBySimulation devuelve el informe más reciente de la simulación.
// Python devolvía el primero según el orden de os.listdir; aquí se ordena por
// created_at para que "el informe de la simulación" sea determinista.
func (m *Manager) GetReportBySimulation(simulationID string) *Report {
	reports := m.ListReports(simulationID, 0)
	if len(reports) == 0 {
		return nil
	}
	return reports[0]
}

// ListReports replica list_reports (created_at desc, límite opcional).
func (m *Manager) ListReports(simulationID string, limit int) []*Report {
	entries, err := os.ReadDir(m.ReportsDir)
	if err != nil {
		return []*Report{}
	}
	reports := []*Report{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r := m.GetReport(e.Name())
		if r == nil {
			continue
		}
		if simulationID != "" && r.SimulationID != simulationID {
			continue
		}
		reports = append(reports, r)
	}
	sort.SliceStable(reports, func(i, j int) bool {
		return reports[i].CreatedAt > reports[j].CreatedAt
	})
	if limit > 0 && len(reports) > limit {
		reports = reports[:limit]
	}
	return reports
}

// DeleteReport replica delete_report: borra la carpeta completa.
func (m *Manager) DeleteReport(reportID string) bool {
	if !safeID(reportID) {
		return false
	}
	dir := m.FolderPath(reportID)
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return os.RemoveAll(dir) == nil
	}
	return false
}

// UpdateProgress replica update_progress.
func (m *Manager) UpdateProgress(reportID, status string, progress int, message string, currentSection *string, completed []string) error {
	if _, err := m.EnsureFolder(reportID); err != nil {
		return err
	}
	if completed == nil {
		completed = []string{}
	}
	data := map[string]any{
		"status":             status,
		"progress":           progress,
		"message":            message,
		"current_section":    currentSection,
		"completed_sections": completed,
		"updated_at":         timex.Now(),
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return fsx.WriteFileAtomic(m.ProgressPath(reportID), b, 0o644)
}

// GetProgress replica get_progress (nil si no existe).
func (m *Manager) GetProgress(reportID string) map[string]any {
	if !safeID(reportID) {
		return nil
	}
	raw, err := os.ReadFile(m.ProgressPath(reportID))
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// GetGeneratedSections replica get_generated_sections.
func (m *Manager) GetGeneratedSections(reportID string) []map[string]any {
	if !safeID(reportID) {
		return []map[string]any{}
	}
	entries, err := os.ReadDir(m.FolderPath(reportID))
	if err != nil {
		return []map[string]any{}
	}
	names := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "section_") && strings.HasSuffix(name, ".md") && !e.IsDir() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(m.FolderPath(reportID), name))
		if err != nil {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "section_"), ".md"))
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"filename":      name,
			"section_index": index,
			"content":       string(content),
		})
	}
	return out
}

// SaveOutline replica save_outline.
func (m *Manager) SaveOutline(reportID string, outline *Outline) error {
	if _, err := m.EnsureFolder(reportID); err != nil {
		return err
	}
	b, err := json.MarshalIndent(outline.ToDict(), "", "  ")
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return fsx.WriteFileAtomic(m.OutlinePath(reportID), b, 0o644)
}

// SaveSection replica save_section: limpia títulos y escribe section_NN.md.
func (m *Manager) SaveSection(reportID string, index int, section Section) (string, error) {
	if _, err := m.EnsureFolder(reportID); err != nil {
		return "", err
	}
	cleaned := CleanSectionContent(section.Content, section.Title)
	md := "## " + section.Title + "\n\n"
	if cleaned != "" {
		md += cleaned + "\n\n"
	}
	path := m.SectionPath(reportID, index)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := fsx.WriteFileAtomic(path, []byte(md), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

var reHeading = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

// CleanSectionContent replica _clean_section_content: elimina el título
// repetido al inicio y convierte cualquier heading restante en negrita.
func CleanSectionContent(content, sectionTitle string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	cleaned := []string{}
	skipNextEmpty := false
	normalize := func(s string) string { return strings.ReplaceAll(s, " ", "") }

	for i, line := range lines {
		stripped := strings.TrimSpace(line)
		if match := reHeading.FindStringSubmatch(stripped); match != nil {
			title := strings.TrimSpace(match[2])
			if i < 5 && (title == sectionTitle || normalize(title) == normalize(sectionTitle)) {
				skipNextEmpty = true
				continue
			}
			cleaned = append(cleaned, "**"+title+"**", "")
			continue
		}
		if skipNextEmpty && stripped == "" {
			skipNextEmpty = false
			continue
		}
		skipNextEmpty = false
		cleaned = append(cleaned, line)
	}

	// Quita líneas vacías y separadores iniciales.
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[0]) == "" {
		cleaned = cleaned[1:]
	}
	for len(cleaned) > 0 {
		first := strings.TrimSpace(cleaned[0])
		if first != "---" && first != "***" && first != "___" {
			break
		}
		cleaned = cleaned[1:]
		for len(cleaned) > 0 && strings.TrimSpace(cleaned[0]) == "" {
			cleaned = cleaned[1:]
		}
	}
	return strings.Join(cleaned, "\n")
}

// AssembleFullReport replica assemble_full_report + _post_process_report.
func (m *Manager) AssembleFullReport(reportID string, outline *Outline) (string, error) {
	if _, err := m.EnsureFolder(reportID); err != nil {
		return "", err
	}
	md := "# " + outline.Title + "\n\n> " + outline.Summary + "\n\n---\n\n"
	for _, section := range m.GetGeneratedSections(reportID) {
		md += str(section["content"])
	}
	md = PostProcessReport(md, outline)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := fsx.WriteFileAtomic(m.MarkdownPath(reportID), []byte(md), 0o644); err != nil {
		return "", err
	}
	return md, nil
}

// PostProcessReport replica _post_process_report: conserva # título y
// ## capítulos, convierte ### en negrita, deduplica headings y limita los
// bloques de líneas vacías a dos.
func PostProcessReport(content string, outline *Outline) string {
	lines := strings.Split(content, "\n")
	sectionTitles := map[string]bool{}
	for _, s := range outline.Sections {
		sectionTitles[s.Title] = true
	}

	processed := []string{}
	prevWasHeading := false
	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		match := reHeading.FindStringSubmatch(stripped)
		if match != nil {
			level := len(match[1])
			title := strings.TrimSpace(match[2])

			duplicate := false
			start := len(processed) - 5
			if start < 0 {
				start = 0
			}
			for _, prev := range processed[start:] {
				if m := reHeading.FindStringSubmatch(strings.TrimSpace(prev)); m != nil {
					if strings.TrimSpace(m[2]) == title {
						duplicate = true
						break
					}
				}
			}
			if duplicate {
				continue
			}

			switch {
			case level == 1 && title == outline.Title:
				processed = append(processed, line)
				prevWasHeading = true
			case level == 1 && sectionTitles[title]:
				processed = append(processed, "## "+title)
				prevWasHeading = true
			case level == 2 && (sectionTitles[title] || title == outline.Title):
				processed = append(processed, line)
				prevWasHeading = true
			default:
				processed = append(processed, "**"+title+"**", "")
				prevWasHeading = false
			}
			continue
		}

		if stripped == "---" && prevWasHeading {
			continue
		}
		if stripped == "" && prevWasHeading {
			if len(processed) > 0 && strings.TrimSpace(processed[len(processed)-1]) != "" {
				processed = append(processed, line)
			}
			prevWasHeading = false
			continue
		}
		processed = append(processed, line)
		prevWasHeading = false
	}

	result := []string{}
	emptyCount := 0
	for _, line := range processed {
		if strings.TrimSpace(line) == "" {
			emptyCount++
			if emptyCount <= 2 {
				result = append(result, line)
			}
			continue
		}
		emptyCount = 0
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// AppendAgentLog agrega una entrada JSONL (agent_log.jsonl).
func (m *Manager) AppendAgentLog(reportID string, entry map[string]any) error {
	if _, err := m.EnsureFolder(reportID); err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.OpenFile(m.AgentLogPath(reportID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// AppendConsoleLog agrega una línea de texto (console_log.txt).
func (m *Manager) AppendConsoleLog(reportID, line string) error {
	if _, err := m.EnsureFolder(reportID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.OpenFile(m.ConsoleLogPath(reportID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// GetAgentLog replica get_agent_log: líneas desde from_line (0-based),
// saltando las que no son JSON válido.
func (m *Manager) GetAgentLog(reportID string, fromLine int) map[string]any {
	logs := []map[string]any{}
	total := 0
	if !safeID(reportID) {
		return map[string]any{"logs": logs, "total_lines": 0, "from_line": 0, "has_more": false}
	}
	f, err := os.Open(m.AgentLogPath(reportID))
	if err != nil {
		return map[string]any{"logs": logs, "total_lines": 0, "from_line": 0, "has_more": false}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	i := 0
	for scanner.Scan() {
		if i >= fromLine {
			var entry map[string]any
			if json.Unmarshal(scanner.Bytes(), &entry) == nil {
				logs = append(logs, entry)
			}
		}
		i++
	}
	total = i
	return map[string]any{
		"logs":        logs,
		"total_lines": total,
		"from_line":   fromLine,
		"has_more":    false,
	}
}

// GetAgentLogStream replica get_agent_log_stream.
func (m *Manager) GetAgentLogStream(reportID string) []map[string]any {
	return m.GetAgentLog(reportID, 0)["logs"].([]map[string]any)
}

// GetConsoleLog replica get_console_log.
func (m *Manager) GetConsoleLog(reportID string, fromLine int) map[string]any {
	logs := []string{}
	if !safeID(reportID) {
		return map[string]any{"logs": logs, "total_lines": 0, "from_line": 0, "has_more": false}
	}
	f, err := os.Open(m.ConsoleLogPath(reportID))
	if err != nil {
		return map[string]any{"logs": logs, "total_lines": 0, "from_line": 0, "has_more": false}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	i := 0
	for scanner.Scan() {
		if i >= fromLine {
			logs = append(logs, strings.TrimRight(scanner.Text(), "\r"))
		}
		i++
	}
	return map[string]any{
		"logs":        logs,
		"total_lines": i,
		"from_line":   fromLine,
		"has_more":    false,
	}
}

// GetConsoleLogStream replica get_console_log_stream.
func (m *Manager) GetConsoleLogStream(reportID string) []string {
	return m.GetConsoleLog(reportID, 0)["logs"].([]string)
}
