// Test de compatibilidad: el port Go debe leer y reimprimir los project.json
// creados por el backend Python de Flask sin perder campos. Usa un proyecto
// real existente como fixture.
package models

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// replicalJsonOriginal: project.json de un proyecto real de la instalación
// Python (con campo user_id, ontology grande, files con filename/size).
func fixtureProjectJSON() string {
	return `{
  "project_id": "proj_04fa48f3631a",
  "name": "Unnamed Project",
  "status": "graph_completed",
  "created_at": "2026-11-02T16:46:28.524678",
  "updated_at": "2026-11-02T16:53:20.012054",
  "files": [
    {"filename": "01_erpgo_ficha_producto.md", "size": 3669},
    {"filename": "02_mercado_venezolano.md", "size": 4518}
  ],
  "total_text_length": 16751,
  "ontology": {"entity_types": [{"name": "Smeowner", "attributes": [{"name": "business_type", "type": "text"}]}], "edge_types": []},
  "analysis_summary": "El texto describe una simulación de mercado para ERPGo.",
  "graph_id": "mirofish_d1cfb66e6d7941b6",
  "graph_build_task_id": "4208102d-bab8-48c6-87af-10f18ce5ce93",
  "zep_batch_id": "batch_2feda507ef1741de",
  "zep_batch_operation_id": "920054b9ec72c85e0587a6473bbe3854196e56d60ec8d0955ff9f845704f1c44",
  "simulation_requirement": "Quiero analizar que modelo de negocio tendra mas exito...",
  "chunk_size": 500,
  "chunk_overlap": 50,
  "user_id": "usr_052d1e5bef06",
  "error": null
}`
}

func TestUnmarshalRoundTripPreservesPythonFields(t *testing.T) {
	src := fixtureProjectJSON()
	var p Project
	if err := json.Unmarshal([]byte(src), &p); err != nil {
		t.Fatalf(".Unmarshal: %v", err)
	}

	if p.ProjectID != "proj_04fa48f3631a" {
		t.Errorf("ProjectID = %q", p.ProjectID)
	}
	if p.Status != ProjectGraphCompleted {
		t.Errorf("Status = %q", p.Status)
	}
	if p.CreatedAt != "2026-11-02T16:46:28.524678" {
		t.Errorf("CreatedAt = %q", p.CreatedAt)
	}
	if len(p.Files) != 2 || p.Files[0].Filename != "01_erpgo_ficha_producto.md" || *p.Files[0].Size != 3669 {
		t.Errorf("Files = %+v", p.Files)
	}
	if p.TotalTextLength != 16751 {
		t.Errorf("TotalTextLength = %d", p.TotalTextLength)
	}
	if p.Ontology == nil {
		t.Error("ontology perdido")
	}
	if p.UserID == nil || *p.UserID != "usr_052d1e5bef06" {
		t.Errorf("UserID = %v", p.UserID)
	}
	if p.GraphID == nil || *p.GraphID != "mirofish_d1cfb66e6d7941b6" {
		t.Errorf("GraphID = %v", p.GraphID)
	}
	if p.ChunkSize != 500 || p.ChunkOverlap != 50 {
		t.Errorf("chunking = %d/%d", p.ChunkSize, p.ChunkOverlap)
	}

	// Round-trip: debe re-serializar manteniendo TODAS las claves de Python.
	out, err := json.Marshal(&p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var srcMap, outMap map[string]any
	_ = json.Unmarshal([]byte(src), &srcMap)
	if err := json.Unmarshal(out, &outMap); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	for _, k := range []string{
		"project_id", "name", "status", "created_at", "updated_at", "files",
		"total_text_length", "ontology", "analysis_summary", "graph_id",
		"graph_build_task_id", "zep_batch_id", "zep_batch_operation_id",
		"simulation_requirement", "chunk_size", "chunk_overlap", "user_id", "error",
	} {
		if _, ok := outMap[k]; !ok {
			t.Errorf("clave %q perdida al re-serializar", k)
		}
	}
	if len(srcMap) != len(outMap) {
		t.Errorf("num claves: python=%d go=%d", len(srcMap), len(outMap))
	}
}

// Defaults de from_dict: sin chunk/chunk_overlap en el JSON → 500/50,
// sin status → created, sin name → Unnamed Project.
func TestUnmarshalPythonDefaults(t *testing.T) {
	minimal := `{"project_id":"proj_abc","files":[]}`
	var p Project
	if err := json.Unmarshal([]byte(minimal), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Status != ProjectCreated || p.Name != "Unnamed Project" || p.ChunkSize != 500 || p.ChunkOverlap != 50 {
		t.Errorf("defaults: %+v", p)
	}
}

// list/del/save sobre un tmp dir: lifecycle completo del ProjectManager.
func TestProjectManagerLifecycle(t *testing.T) {
	uploads := t.TempDir()
	m := NewProjectManager(uploads)

	p, err := m.CreateProject("Prueba", "usr_1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.filesDir(p.ProjectID))); err != nil {
		t.Fatalf("files dir no creado")
	}

	// save_file_to_project
	info, err := m.SaveFileToProject(p.ProjectID, "Mi Doc.pdf", bytes.NewReader([]byte("contenido")))
	if err != nil {
		t.Fatalf("SaveFileToProject: %v", err)
	}
	if info["original_filename"] != "Mi Doc.pdf" {
		t.Errorf("info = %v", info)
	}
	if sz := info["saved_filename"].(string); filepath.Ext(sz) != ".pdf" || len(sz) != 12 {
		t.Errorf("saved_filename = %q", sz)
	}

	// extracted text
	m.SaveExtractedText(p.ProjectID, "hola")
	if s := m.GetExtractedText(p.ProjectID); s == nil || *s != "hola" {
		t.Errorf("GetExtractedText = %v", s)
	}

	// list filtrado por user
	if got := len(m.ListProjects(0, "usr_1")); got != 1 {
		t.Errorf("ListProjects usr_1 = %d", got)
	}
	if got := len(m.ListProjects(0, "otro")); got != 0 {
		t.Errorf("ListProjects otro = %d", got)
	}

	// delete
	if !m.DeleteProject(p.ProjectID) {
		t.Error("DeleteProject false")
	}
	if m.GetProject(p.ProjectID) != nil {
		t.Error("proyecto aún existe tras delete")
	}
}

// TaskManager: create/update/complete/fail/list.
func TestTaskManagerLifecycle(t *testing.T) {
	tm := GetTaskManager()
	id := tm.CreateTask("test", map[string]any{"k": "v"})

	// update parcial
	prog := 55
	msg := "avanzando"
	tm.UpdateTask(id, TaskUpdate{Progress: &prog, Message: &msg})
	task, ok := tm.GetTask(id)
	if !ok || task.Status != TaskPending {
		t.Fatalf("tras update parcial status debe seguir pending: %v %v", task, ok)
	}

	tm.CompleteTask(id, map[string]any{"ok": true})
	task, _ = tm.GetTask(id)
	if task.Status != TaskCompleted || task.Progress != 100 || task.ErrorMsg != nil {
		t.Errorf("complete: %+v", task)
	}

	// list sample con isoformat microsegundos
	list := tm.ListTasks("test")
	if len(list) == 0 || list[0].Status != "completed" {
		t.Fatalf("list = %+v", list)
	}
	if len(list[0].CreatedAt) < 20 || list[0].CreatedAt[10] != 'T' {
		t.Errorf("isoformat = %q", list[0].CreatedAt)
	}

	// fail
	fid := tm.CreateTask("test2", nil)
	tm.FailTask(fid, "boom")
	task, _ = tm.GetTask(fid)
	if task.Status != TaskFailed || task.ErrorMsg == nil || *task.ErrorMsg != "boom" {
		t.Errorf("fail: %+v", task)
	}
}
