// Package reportpdf — informe final en PDF con gráficos (opinión, tendencias,
// entidades y actividad de la simulación). Se genera en el binario con librerías
// Go (gofpdf + gonum/plot), sin dependencias externas.
package reportpdf

import (
	"fmt"
	"image/color"
	"os"
	"sort"
	"strings"

	"github.com/go-pdf/fpdf"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"

	"mirofish/internal/locale"
	"mirofish/internal/oasis"
	"mirofish/internal/report"
)

// Data — insumos del PDF.
type Data struct {
	Report   *report.Report
	Stats    map[string]any // report.Tools.GraphStatistics
	Posts    []oasis.Post
	Comments []oasis.Comment
	Actions  []oasis.ActionRecord
	Loc      string // es|en (etiquetas de gráficos)
}

// Generate escribe el PDF en out.
func Generate(data Data, out interface{ Write([]byte) (int, error) }) error {
	loc := data.Loc
	if loc == "" {
		loc = "es"
	}
	t := func(key string) string { return locale.T(loc, "pdf."+key, nil) }

	pdf := fpdf.New("P", "mm", "A4", "")
	title := t("reportTitle")
	if data.Report.Outline != nil && data.Report.Outline.Title != "" {
		title = data.Report.Outline.Title
	}
	pdf.SetTitle(title, false)
	pdf.SetAuthor("MiroFish", false)
	pdf.SetMargins(18, 18, 18)
	pdf.SetAutoPageBreak(true, 18)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 8, fmt.Sprintf("%s  ·  %d", t("confidential"), pdf.PageNo()), "", 0, "C", false, 0, "")
	})

	// ===== Portada =====
	pdf.AddPage()
	pdf.SetY(55)
	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(20, 20, 30)
	writeWrapped(pdf, title, 0)
	pdf.Ln(6)
	pdf.SetFont("Helvetica", "", 11)
	pdf.SetTextColor(90, 90, 100)
	if data.Report.SimulationRequirement != "" {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.Cell(0, 6, t("scenario")+":")
		pdf.Ln(6)
		pdf.SetFont("Helvetica", "", 10)
		writeWrapped(pdf, data.Report.SimulationRequirement, 0)
		pdf.Ln(8)
	}
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(120, 120, 130)
	pdf.Cell(0, 5, fmt.Sprintf("%s: %s", t("reportId"), data.Report.ReportID))
	pdf.Ln(5)
	pdf.Cell(0, 5, fmt.Sprintf("%s: %s", t("simulationId"), data.Report.SimulationID))
	pdf.Ln(5)
	pdf.Cell(0, 5, fmt.Sprintf("%s: %s", t("created"), data.Report.CreatedAt))
	if data.Report.CompletedAt != "" {
		pdf.Ln(5)
		pdf.Cell(0, 5, fmt.Sprintf("%s: %s", t("completed"), data.Report.CompletedAt))
	}

	// ===== Resumen =====
	if data.Report.Outline != nil && data.Report.Outline.Summary != "" {
		pdf.AddPage()
		heading(pdf, t("summary"), 14)
		pdf.SetFont("Helvetica", "", 10.5)
		pdf.SetTextColor(40, 40, 50)
		writeWrapped(pdf, data.Report.Outline.Summary, 0)
	}

	// ===== Gráficos =====
	pdf.AddPage()
	heading(pdf, t("metrics"), 14)
	pdf.SetFont("Helvetica", "", 9.5)
	pdf.SetTextColor(90, 90, 100)
	writeWrapped(pdf, t("metricsIntro"), 0)
	pdf.Ln(4)

	type chart struct {
		title string
		file  string
	}
	charts := []chart{}
	if c, err := chartActionsPerRound(data.Actions, t("actionsPerRound")); err == nil {
		charts = append(charts, c)
	}
	if c, err := chartActionTypes(data.Actions, t("actionTypes")); err == nil {
		charts = append(charts, c)
	}
	if c, err := chartTopPosts(data.Posts, t("topPosts")); err == nil {
		charts = append(charts, c)
	}
	if c, err := chartEntityTypes(data.Stats, t("entityTypes")); err == nil {
		charts = append(charts, c)
	}
	if c, err := chartRelations(data.Stats, t("relationTypes")); err == nil {
		charts = append(charts, c)
	}

	for i, c := range charts {
		if pdf.GetY() > 190 {
			pdf.AddPage()
		}
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(30, 30, 40)
		pdf.Cell(0, 6, c.title)
		pdf.Ln(6)
		pdf.Image(c.file, 18, pdf.GetY(), 170, 0, false, "", 0, "")
		pdf.Ln(92)
		_ = i
	}

	// ===== Contenido del informe =====
	pdf.AddPage()
	heading(pdf, t("content"), 14)
	if data.Report.Outline != nil {
		for _, sec := range data.Report.Outline.Sections {
			if pdf.GetY() > 250 {
				pdf.AddPage()
			}
			pdf.SetFont("Helvetica", "B", 12)
			pdf.SetTextColor(25, 25, 35)
			writeWrapped(pdf, sec.Title, 0)
			pdf.Ln(1)
			pdf.SetFont("Helvetica", "I", 9.5)
			pdf.SetTextColor(100, 100, 110)
			pdf.Ln(2)
		}
	}
	// Cuerpo markdown (secciones ya generadas)
	if data.Report.MarkdownContent != "" {
		renderMarkdown(pdf, data.Report.MarkdownContent)
	}

	return pdf.Output(out)
}

// ===== helpers de layout =====

func heading(pdf *fpdf.Fpdf, text string, size float64) {
	pdf.SetFont("Helvetica", "B", size)
	pdf.SetTextColor(20, 20, 30)
	writeWrapped(pdf, text, 0)
	pdf.Ln(2)
	pdf.SetDrawColor(255, 69, 0)
	pdf.Line(18, pdf.GetY(), 60, pdf.GetY())
	pdf.Ln(4)
}

func writeWrapped(pdf *fpdf.Fpdf, text string, width float64) {
	if width <= 0 {
		width = 174
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line == "" {
			pdf.Ln(4)
			continue
		}
		lines := pdf.SplitLines([]byte(line), width)
		for _, l := range lines {
			pdf.CellFormat(width, 5.2, string(l), "", 1, "L", false, 0, "")
		}
	}
}

// renderMarkdown — renderiza markdown plano: # títulos, - bullets, > citas,
// **negrita** se limpia; sin soporte de tablas ni código.
func renderMarkdown(pdf *fpdf.Fpdf, md string) {
	for _, raw := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		switch {
		case strings.HasPrefix(line, "#### "):
			pdf.Ln(1)
			pdf.SetFont("Helvetica", "B", 10)
			pdf.SetTextColor(40, 40, 50)
			writeWrapped(pdf, cleanInline(strings.TrimPrefix(line, "#### ")), 0)
		case strings.HasPrefix(line, "### "):
			pdf.Ln(1)
			pdf.SetFont("Helvetica", "B", 11)
			pdf.SetTextColor(30, 30, 40)
			writeWrapped(pdf, cleanInline(strings.TrimPrefix(line, "### ")), 0)
		case strings.HasPrefix(line, "## "):
			pdf.Ln(1)
			pdf.SetFont("Helvetica", "B", 12)
			pdf.SetTextColor(25, 25, 35)
			writeWrapped(pdf, cleanInline(strings.TrimPrefix(line, "## ")), 0)
		case strings.HasPrefix(line, "# "):
			pdf.Ln(1)
			pdf.SetFont("Helvetica", "B", 13)
			pdf.SetTextColor(20, 20, 30)
			writeWrapped(pdf, cleanInline(strings.TrimPrefix(line, "# ")), 0)
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			pdf.SetFont("Helvetica", "", 10)
			pdf.SetTextColor(50, 50, 60)
			pdf.Cell(6, 5, "•")
			writeWrapped(pdf, cleanInline(strings.TrimPrefix(line, "- ")), 168)
		case strings.HasPrefix(line, "> "):
			pdf.SetFont("Helvetica", "I", 10)
			pdf.SetTextColor(90, 90, 100)
			pdf.SetLeftMargin(24)
			writeWrapped(pdf, cleanInline(strings.TrimPrefix(line, "> ")), 156)
			pdf.SetLeftMargin(18)
		case line == "":
			pdf.Ln(2.5)
		default:
			pdf.SetFont("Helvetica", "", 10)
			pdf.SetTextColor(50, 50, 60)
			writeWrapped(pdf, cleanInline(line), 0)
		}
	}
}

// cleanInline elimina marcado inline (**, `, []()).
func cleanInline(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	if i := strings.Index(s, "]("); i >= 0 {
		if j := strings.LastIndex(s[:i], "["); j >= 0 {
			if k := strings.Index(s[i:], ")"); k >= 0 {
				s = s[:j] + s[j+1:i] + s[i+k+1:]
			}
		}
	}
	return s
}

// ===== generación de gráficos (PNG temporales) =====

func renderChart(path, title, xLabel, yLabel string, horizontal bool, labels []string, values []float64) (string, error) {
	p := plot.New()
	p.Title.Text = title
	p.Title.TextStyle.Font.Size = vg.Points(13)
	if horizontal {
		p.Y.Label.Text = yLabel
	} else {
		p.X.Label.Text = xLabel
		p.Y.Label.Text = yLabel
	}
	if len(values) == 0 {
		return "", fmt.Errorf("sin datos")
	}
	// Barras verticales o horizontales
	chart, err := plotter.NewBarChart(plotter.Values(values), vg.Points(14))
	if err != nil {
		return "", err
	}
	chart.Color = color.RGBA{R: 255, G: 69, B: 0, A: 255}
	if horizontal {
		chart.Horizontal = true
		p.NominalY(labels...)
	} else {
		p.NominalX(labels...)
	}
	p.Add(chart)
	p.Add(plotter.NewGrid())
	if err := p.Save(8*vg.Inch, 3.5*vg.Inch, path); err != nil {
		return "", err
	}
	return path, nil
}

func chartActionsPerRound(actions []oasis.ActionRecord, title string) (struct {
	title string
	file  string
}, error) {
	type res = struct {
		title string
		file  string
	}
	if len(actions) == 0 {
		return res{}, fmt.Errorf("sin acciones")
	}
	rounds := map[int]int{}
	for _, a := range actions {
		rounds[a.Round]++
	}
	keys := sortedKeys(rounds)
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, fmt.Sprintf("%d", k))
		values = append(values, float64(rounds[k]))
	}
	path := tempFile("actions_round.png")
	if _, err := renderChart(path, title, "Ronda", "Acciones", false, labels, values); err != nil {
		return res{}, err
	}
	return res{title: title, file: path}, nil
}

func chartActionTypes(actions []oasis.ActionRecord, title string) (struct {
	title string
	file  string
}, error) {
	type res = struct {
		title string
		file  string
	}
	counts := map[string]int{}
	for _, a := range actions {
		counts[strings.ToUpper(a.ActionType)]++
	}
	type kv struct {
		k string
		v int
	}
	list := []kv{}
	for k, v := range counts {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	if len(list) == 0 {
		return res{}, fmt.Errorf("sin tipos")
	}
	labels := make([]string, 0, len(list))
	values := make([]float64, 0, len(list))
	for _, it := range list {
		labels = append(labels, it.k)
		values = append(values, float64(it.v))
	}
	path := tempFile("action_types.png")
	if _, err := renderChart(path, title, "", "Acciones", true, labels, values); err != nil {
		return res{}, err
	}
	return res{title: title, file: path}, nil
}

func chartTopPosts(posts []oasis.Post, title string) (struct {
	title string
	file  string
}, error) {
	type res = struct {
		title string
		file  string
	}
	if len(posts) == 0 {
		return res{}, fmt.Errorf("sin posts")
	}
	sorted := append([]oasis.Post{}, posts...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].NumLikes+sorted[i].NumShares+sorted[i].NumDislikes >
			sorted[j].NumLikes+sorted[j].NumShares+sorted[j].NumDislikes
	})
	if len(sorted) > 8 {
		sorted = sorted[:8]
	}
	labels := make([]string, 0, len(sorted))
	values := make([]float64, 0, len(sorted))
	for i := len(sorted) - 1; i >= 0; i-- {
		p := sorted[i]
		label := strings.TrimSpace(p.Content)
		if len([]rune(label)) > 42 {
			label = string([]rune(label)[:42]) + "…"
		}
		labels = append(labels, label)
		values = append(values, float64(p.NumLikes+p.NumShares+p.NumDislikes))
	}
	path := tempFile("top_posts.png")
	if _, err := renderChart(path, title, "", "Interacciones", true, labels, values); err != nil {
		return res{}, err
	}
	return res{title: title, file: path}, nil
}

func chartEntityTypes(stats map[string]any, title string) (struct {
	title string
	file  string
}, error) {
	return chartCountMap(stats, "entity_types", title)
}

func chartRelations(stats map[string]any, title string) (struct {
	title string
	file  string
}, error) {
	return chartCountMap(stats, "relation_types", title)
}

func chartCountMap(stats map[string]any, key, title string) (struct {
	title string
	file  string
}, error) {
	type res = struct {
		title string
		file  string
	}
	if stats == nil {
		return res{}, fmt.Errorf("sin stats")
	}
	raw, ok := stats[key].(map[string]int)
	if !ok || len(raw) == 0 {
		return res{}, fmt.Errorf("sin datos")
	}
	type kv struct {
		k string
		v int
	}
	list := []kv{}
	for k, v := range raw {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	if len(list) > 10 {
		list = list[:10]
	}
	labels := make([]string, 0, len(list))
	values := make([]float64, 0, len(list))
	for _, it := range list {
		labels = append(labels, it.k)
		values = append(values, float64(it.v))
	}
	path := tempFile(key + ".png")
	if _, err := renderChart(path, title, "", "Cantidad", true, labels, values); err != nil {
		return res{}, err
	}
	return res{title: title, file: path}, nil
}

func sortedKeys(m map[int]int) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// tempFile crea un PNG temporal con prefijo y devuelve la ruta.
var chartDir = func() string {
	dir, err := os.MkdirTemp("", "mirofish_pdf_")
	if err != nil {
		return ""
	}
	return dir
}()

func tempFile(name string) string {
	if chartDir == "" {
		return name
	}
	return chartDir + string('/') + name
}
