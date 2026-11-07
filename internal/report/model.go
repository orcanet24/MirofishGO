// Package report — port de backend/app/services/report_agent.py.
//
// Modelo y persistencia del informe: mismo layout de archivos que Python
// (reports/<report_id>/{meta,outline,progress}.json, section_NN.md,
// full_report.md, agent_log.jsonl, console_log.txt) para que el binario Go y
// el backend Flask puedan leer los mismos informes sin migración.
package report

// Status replica ReportStatus.
type Status string

const (
	StatusPending    Status = "pending"
	StatusPlanning   Status = "planning"
	StatusGenerating Status = "generating"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// Section replica ReportSection.
type Section struct {
	Title   string
	Content string
}

// ToDict replica ReportSection.to_dict.
func (s Section) ToDict() map[string]any {
	return map[string]any{"title": s.Title, "content": s.Content}
}

// ToMarkdown replica ReportSection.to_markdown.
func (s Section) ToMarkdown(level int) string {
	md := heading(level) + " " + s.Title + "\n\n"
	if s.Content != "" {
		md += s.Content + "\n\n"
	}
	return md
}

func heading(level int) string {
	if level < 1 {
		level = 1
	}
	out := make([]rune, level)
	for i := range out {
		out[i] = '#'
	}
	return string(out)
}

// Outline replica ReportOutline.
type Outline struct {
	Title    string
	Summary  string
	Sections []Section
}

// ToDict replica ReportOutline.to_dict.
func (o *Outline) ToDict() map[string]any {
	sections := make([]map[string]any, 0, len(o.Sections))
	for _, s := range o.Sections {
		sections = append(sections, s.ToDict())
	}
	return map[string]any{
		"title":    o.Title,
		"summary":  o.Summary,
		"sections": sections,
	}
}

// ToMarkdown replica ReportOutline.to_markdown.
func (o *Outline) ToMarkdown() string {
	md := "# " + o.Title + "\n\n"
	md += "> " + o.Summary + "\n\n"
	for _, s := range o.Sections {
		md += s.ToMarkdown(2)
	}
	return md
}

// Report replica el dataclass Report.
type Report struct {
	ReportID              string
	SimulationID          string
	GraphID               string
	SimulationRequirement string
	Status                Status
	Outline               *Outline
	MarkdownContent       string
	CreatedAt             string
	CompletedAt           string
	Error                 *string
	UserID                *string
}

// ToDict replica Report.to_dict (incluidas las claves null).
func (r *Report) ToDict() map[string]any {
	var outline any
	if r.Outline != nil {
		outline = r.Outline.ToDict()
	}
	return map[string]any{
		"report_id":              r.ReportID,
		"simulation_id":          r.SimulationID,
		"graph_id":               r.GraphID,
		"simulation_requirement": r.SimulationRequirement,
		"status":                 string(r.Status),
		"outline":                outline,
		"markdown_content":       r.MarkdownContent,
		"created_at":             r.CreatedAt,
		"completed_at":           r.CompletedAt,
		"error":                  r.Error,
		"user_id":                r.UserID,
	}
}

// FromDict reconstruye un Report desde meta.json.
func FromDict(data map[string]any) *Report {
	r := &Report{
		ReportID:              str(data["report_id"]),
		SimulationID:          str(data["simulation_id"]),
		GraphID:               str(data["graph_id"]),
		SimulationRequirement: str(data["simulation_requirement"]),
		Status:                Status(str(data["status"])),
		MarkdownContent:       str(data["markdown_content"]),
		CreatedAt:             str(data["created_at"]),
		CompletedAt:           str(data["completed_at"]),
	}
	if e, ok := data["error"].(string); ok {
		r.Error = &e
	}
	if u, ok := data["user_id"].(string); ok {
		r.UserID = &u
	}
	if outlineData, ok := data["outline"].(map[string]any); ok {
		outline := &Outline{Title: str(outlineData["title"]), Summary: str(outlineData["summary"])}
		if list, ok := outlineData["sections"].([]any); ok {
			for _, raw := range list {
				if m, ok := raw.(map[string]any); ok {
					outline.Sections = append(outline.Sections, Section{
						Title:   str(m["title"]),
						Content: str(m["content"]),
					})
				}
			}
		}
		r.Outline = outline
	}
	return r
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
