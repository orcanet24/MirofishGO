// Package models — port of backend/app/models/project.py.
//
// Project persistido como JSON en uploads/projects/<id>/project.json con el
// MISMO formato de campo (snake_case, nulls explícitos) que emite la app
// Flask: los proyectos creados con el backend Python se leen sin migración.
package models

import (
	"encoding/json"
)

// ProjectStatus replica el enum Python (valores string en JSON).
type ProjectStatus string

const (
	ProjectCreated           ProjectStatus = "created"            // recién creado, archivos subidos
	ProjectOntologyGenerated ProjectStatus = "ontology_generated" // ontología generada
	ProjectGraphBuilding     ProjectStatus = "graph_building"     // grafo en construcción
	ProjectGraphCompleted    ProjectStatus = "graph_completed"    // grafo construido
	ProjectFailed            ProjectStatus = "failed"             // fallido
)

// ProjectFile replica Project.files[i] de Python ({"filename", "size"} y,
// en metadatos existentes, "path"): omitempty conservan compatibilidad.
type ProjectFile struct {
	Filename string  `json:"filename"`
	Path     *string `json:"path,omitempty"`
	Size     *int64  `json:"size,omitempty"`
}

// Project — orden de campos y nombres JSON idénticos a Project.to_dict().
// Los campos opcionales usan puntero para serializar null como Python.
type Project struct {
	ProjectID string        `json:"project_id"`
	Name      string        `json:"name"`
	Status    ProjectStatus `json:"status"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`

	Files           []ProjectFile `json:"files"`
	TotalTextLength int           `json:"total_text_length"`

	Ontology        json.RawMessage `json:"ontology"` // dict o null
	AnalysisSummary *string         `json:"analysis_summary"`

	GraphID        *string `json:"graph_id"`
	GraphBuildTask *string `json:"graph_build_task_id"`
	ZepBatchID     *string `json:"zep_batch_id"`
	ZepBatchOpID   *string `json:"zep_batch_operation_id"`

	SimulationRequirement *string `json:"simulation_requirement"`
	ChunkSize             int     `json:"chunk_size"`
	ChunkOverlap          int     `json:"chunk_overlap"`

	UserID *string `json:"user_id"`

	// StudyType — tipo de estudio (político, médico, marca...): define qué
	// dimensiones de perfil son relevantes para los agentes.
	StudyType string `json:"study_type,omitempty"`

	Error *string `json:"error"`
}

// projectAlias evita recursión en UnmarshalJSON.
type projectAlias Project

// ToDict replica Project.to_dict(): mismo conjunto de claves y orden.
func (p *Project) ToDict() map[string]any {
	b, err := json.Marshal(p)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// UnmarshalJSON replica Project.from_dict: los defaults 500/50 y el status
// "created" en Python se aplican SOLO cuando el campo no está presente.
func (p *Project) UnmarshalJSON(b []byte) error {
	var a projectAlias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*p = Project(a)

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	if _, ok := probe["status"]; !ok {
		p.Status = ProjectCreated
	}
	if _, ok := probe["name"]; !ok {
		p.Name = "Unnamed Project"
	}
	if _, ok := probe["chunk_size"]; !ok {
		p.ChunkSize = 500
	}
	if _, ok := probe["chunk_overlap"]; !ok {
		p.ChunkOverlap = 50
	}
	if _, ok := probe["files"]; !ok {
		p.Files = []ProjectFile{}
	}
	return nil
}
