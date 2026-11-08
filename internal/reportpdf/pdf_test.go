package reportpdf_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"mirofish/internal/oasis"
	"mirofish/internal/report"
	"mirofish/internal/reportpdf"
)

func TestGenerate(t *testing.T) {
	rep := &report.Report{
		ReportID:              "report_test000",
		SimulationID:          "sim_test",
		GraphID:               "mirofish_test",
		SimulationRequirement: "Cómo recibe la clientela una suba del 15% en autopartes",
		Status:                report.StatusCompleted,
		CreatedAt:             "2026-11-07T12:00:00Z",
		CompletedAt:           "2026-11-07T12:05:00Z",
		Outline: &report.Outline{
			Title:   "Informe de prueba de predicción",
			Summary: "Resumen ejecutivo de la simulación.",
			Sections: []report.Section{
				{Title: "Hallazgos principales", Content: "Texto de la sección uno."},
				{Title: "Riesgos", Content: "Texto de la sección dos."},
			},
		},
		MarkdownContent: "## Hallazgos principales\n\nLa simulación muestra rechazo inicial.\n\n- Dato 1\n- Dato 2\n",
	}
	posts := []oasis.Post{
		{PostID: 1, UserID: 0, Content: "No puedo pagar las autopartes, esto es el colmo", NumLikes: 42, NumShares: 7},
		{PostID: 2, UserID: 1, Content: "El gobierno debe controlar los precios ya", NumLikes: 18, NumDislikes: 3},
	}
	comments := []oasis.Comment{
		{CommentID: 1, PostID: 1, UserID: 2, Content: "Totalmente de acuerdo"},
	}
	actions := []oasis.ActionRecord{
		{Round: 1, ActionType: "CREATE_POST", AgentName: "Ana"},
		{Round: 1, ActionType: "LIKE_POST", AgentName: "Luis"},
		{Round: 2, ActionType: "REPOST", AgentName: "Ana"},
		{Round: 2, ActionType: "CREATE_POST", AgentName: "Marta"},
	}
	stats := map[string]any{
		"total_nodes":    12,
		"total_edges":    30,
		"entity_types":   map[string]int{"Customer": 4, "BusinessOwner": 3, "MediaOutlet": 2},
		"relation_types": map[string]int{"TARGETS": 8, "ENGAGES_WITH": 12},
	}

	var buf bytes.Buffer
	if err := reportpdf.Generate(reportpdf.Data{
		Report: rep, Stats: stats, Posts: posts, Comments: comments, Actions: actions, Loc: "es",
	}, &buf); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.Bytes()
	if len(out) < 1000 {
		t.Fatalf("PDF demasiado pequeño: %d bytes", len(out))
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatal("la salida no empieza con %PDF")
	}
	t.Logf("PDF OK: %d bytes, secciones en contenido: %v", len(out), strings.Contains(string(out), "PDF"))

	if path := os.Getenv("MIROFISH_PDF_OUT"); path != "" {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("no se pudo escribir el PDF de muestra: %v", err)
		}
	}
}
