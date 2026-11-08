// Port de backend/app/models/project.py — clase ProjectManager.
//
// Persistencia 100% en disco como el original:
//
//	uploads/projects/
//	  proj_<12hex>/project.json         metadatos
//	  proj_<12hex>/files/<8hex>.ext     archivos subidos
//	  proj_<12hex>/extracted_text.txt   texto extraído concatenado
//
// El JSON emitido/leído es compatible byte a byte con el backend Python.
package models

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"mirofish/internal/fsx"
	"mirofish/internal/timex"
)

// ProjectManager gestiona el ciclo de vida de proyectos en disco.
type ProjectManager struct {
	ProjectsDir string

	mu sync.Mutex // serialize writes de project.json
}

// NewProjectManager crea el gestor sobre <uploads>/projects.
func NewProjectManager(uploadDir string) *ProjectManager {
	return &ProjectManager{ProjectsDir: filepath.Join(uploadDir, "projects")}
}

// projectID new project id: "proj_" + 12 hex chars (uuid4().hex[:12]).
func newProjectID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "proj_" + hex.EncodeToString(b[:])
}

func (m *ProjectManager) projectDir(projectID string) string {
	return filepath.Join(m.ProjectsDir, projectID)
}

func (m *ProjectManager) metaPath(projectID string) string {
	return filepath.Join(m.projectDir(projectID), "project.json")
}

func (m *ProjectManager) filesDir(projectID string) string {
	return filepath.Join(m.projectDir(projectID), "files")
}

func (m *ProjectManager) textPath(projectID string) string {
	return filepath.Join(m.projectDir(projectID), "extracted_text.txt")
}

func (m *ProjectManager) webContextPath(projectID string) string {
	return filepath.Join(m.projectDir(projectID), "web_context.json")
}

// SaveWebContext guarda la investigación web del estudio (contexto factual).
func (m *ProjectManager) SaveWebContext(projectID string, data []byte) {
	dir := m.projectDir(projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = fsx.WriteFileAtomic(m.webContextPath(projectID), data, 0o644)
}

// GetWebContext devuelve el contexto web o nil si no existe.
func (m *ProjectManager) GetWebContext(projectID string) []byte {
	b, err := os.ReadFile(m.webContextPath(projectID))
	if err != nil {
		return nil
	}
	return b
}

// Initdir ensures dirs (equiv _ensure_projects_dir).
func (m *ProjectManager) ensureProjectsDir() {
	_ = os.MkdirAll(m.ProjectsDir, 0o755)
}

// CreateProject crea la estructura de carpetas y persiste el project.json.
// created_at/updated_at usan datetime.now().isoformat() como Python.
func (m *ProjectManager) CreateProject(name, userID string) (*Project, error) {
	m.ensureProjectsDir()

	ID := newProjectID()
	now := timex.Now()

	p := &Project{
		ProjectID: ID,
		Name:      orDefault(name, "Unnamed Project"),
		Status:    ProjectCreated,
		CreatedAt: now,
		UpdatedAt: now,

		Files:           []ProjectFile{},
		TotalTextLength: 0,

		ChunkSize:    500,
		ChunkOverlap: 50,
	}
	if userID != "" {
		uid := userID
		p.UserID = &uid
	}

	if err := os.MkdirAll(m.filesDir(ID), 0o755); err != nil {
		return nil, err
	}
	if err := m.SaveProject(p); err != nil {
		return nil, err
	}
	return p, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// SaveProject actualiza updated_at y reescribe project.json (ensure_ascii=false,
// indent 2; en JSON estándar ya es UTF-8 no escapado con SetEscapeHTML(false)).
// La escritura es atómica (tmp + rename): un corte de luz o una lectura
// concurrente nunca ven un project.json truncado.
func (m *ProjectManager) SaveProject(p *Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p.UpdatedAt = timex.Now()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.projectDir(p.ProjectID), 0o755); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(m.metaPath(p.ProjectID), b, 0o644)
}

// GetProject devuelve el Project si existe; nil=no encontrado (como el Python).
func (m *ProjectManager) GetProject(projectID string) *Project {
	b, err := os.ReadFile(m.metaPath(projectID))
	if err != nil {
		return nil
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return nil
	}
	return &p
}

// Exists reports whether a project path exists.
func (m *ProjectManager) Exists(projectID string) bool {
	_, err := os.Stat(m.projectDir(projectID))
	return err == nil
}

// ListProjects lista proyectos sorted por created_at desc, con límites/usuario.
// limit <=0 = sin límite (cont como limit=None del Python).
func (m *ProjectManager) ListProjects(limit int, userID string) []*Project {
	m.ensureProjectsDir()

	entries, err := os.ReadDir(m.ProjectsDir)
	if err != nil {
		return []*Project{}
	}
	projects := make([]*Project, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := m.GetProject(e.Name())
		if p == nil {
			continue
		}
		if userID != "" && (p.UserID == nil || *p.UserID != userID) {
			continue
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].CreatedAt > projects[j].CreatedAt
	})
	if limit > 0 && len(projects) > limit {
		projects = projects[:limit]
	}
	return projects
}

// FindProjectsByGraphId returns every project referencing graph_id.
func (m *ProjectManager) FindProjectsByGraphId(graphID string) []*Project {
	var out []*Project
	for _, p := range m.ListProjects(0, "") {
		if p.GraphID != nil && *p.GraphID == graphID {
			out = append(out, p)
		}
	}
	return out
}

// DeleteProject remueve el directorio completo (shutil.rmtree). bool = éxito.
// Toma el mismo mutex que las escrituras para no borrar el directorio mientras
// se está persistiendo un project.json.
func (m *ProjectManager) DeleteProject(projectID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.Exists(projectID) {
		return false
	}
	return os.RemoveAll(m.projectDir(projectID)) == nil
}

// SaveFileToProject persiste el archivo con nombre <8hex><ext> y devuelve
// el dict {original_filename, saved_filename, path, size} del Python.
func (m *ProjectManager) SaveFileToProject(projectID, originalFilename string, r io.Reader) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.filesDir(projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(originalFilename))
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	safeFilename := hex.EncodeToString(raw[:]) + ext
	filePath := filepath.Join(dir, safeFilename)

	dst, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	defer dst.Close() // bien: solo cierre
	size, err := io.Copy(dst, r)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"original_filename": originalFilename,
		"saved_filename":    safeFilename,
		"path":              filePath,
		"size":              size,
	}, nil
}

// SaveFileEntry construye la entrada para Project.files ({"filename","size"}).
func SaveFileEntry(filename string, size int64) ProjectFile {
	return ProjectFile{Filename: filename, Size: &size}
}

// SaveExtractedText persiste el texto concatenado del proyecto.
func (m *ProjectManager) SaveExtractedText(projectID, text string) {
	dir := m.projectDir(projectID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = fsx.WriteFileAtomic(m.textPath(projectID), []byte(text), 0o644)
}

// GetExtractedText devuelve el texto o nil si no existe.
func (m *ProjectManager) GetExtractedText(projectID string) *string {
	b, err := os.ReadFile(m.textPath(projectID))
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// GetProjectFiles — rutas de todos los archivos en files/.
func (m *ProjectManager) GetProjectFiles(projectID string) []string {
	dir := m.filesDir(projectID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
