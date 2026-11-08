// Borrado total ("derecho al olvido"): elimina por completo un estudio y todo
// lo que generó — archivos, grafo (cascade MySQL), simulaciones (datos
// sociales + directorios), informes y referencias — sin dejar rastro.
package httpx

import (
	"errors"
	"net/http"
	"strconv"

	"mirofish/internal/locale"
	"mirofish/internal/models"
)

// purgeProject — DELETE /api/graph/project/{project_id}/purge
func (h *GraphHandler) purgeProject(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	projectID := r.PathValue("project_id")

	lock := buildLock(projectID)
	lock.Lock()
	defer lock.Unlock()

	project := h.PM.GetProject(projectID)
	if project == nil {
		JSON(w, http.StatusNotFound, map[string]any{"success": false, "error": locale.T(loc, "api.projectNotFound", map[string]string{"id": projectID})})
		return
	}
	if !h.denyUnlessOwner(r, w, project) {
		return
	}
	if h.projectHasActiveBuild(project) {
		JSON(w, http.StatusConflict, map[string]any{"success": false, "error": locale.T(loc, "api.graphBuilding", nil)})
		return
	}
	if err := h.purgeProjectData(project); err != nil {
		h.Logger.Error("purge project falló", "project", projectID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "no se pudo completar el borrado total"})
		return
	}
	delBuildLock(projectID)
	h.Logger.Info("estudio borrado por completo", "project", projectID, "user", scopeID(r))
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": locale.T(loc, "api.projectPurged", nil),
	})
}

// purgeAll — POST /api/graph/purge-all (borra todos los estudios propios).
func (h *GraphHandler) purgeAll(w http.ResponseWriter, r *http.Request) {
	loc := locale.Normalize(r.Header.Get("Accept-Language"))
	uid := scopeID(r)
	projects := h.PM.ListProjects(100000, uid)
	purged := 0
	for _, p := range projects {
		lock := buildLock(p.ProjectID)
		lock.Lock()
		if err := h.purgeProjectData(p); err != nil {
			h.Logger.Error("purge-all: falló un proyecto", "project", p.ProjectID, "error", err)
			lock.Unlock()
			continue
		}
		delBuildLock(p.ProjectID)
		purged++
		lock.Unlock()
	}
	h.Logger.Info("purge-all completado", "user", uid, "purged", purged, "total", len(projects))
	JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": locale.T(loc, "api.projectsPurged", map[string]string{"count": strconv.Itoa(purged)}),
	})
}

// purgeProjectData — la eliminación real. Sin ownership check (lo hace el
// llamador); debe ejecutarse bajo buildLock del proyecto.
func (h *GraphHandler) purgeProjectData(project *models.Project) error {
	projectID := project.ProjectID

	// 1) Simulaciones del proyecto: informes + datos sociales + directorio.
	if h.Sim != nil {
		for _, sim := range h.Sim.ListSimulations(projectID) {
			simID := sim.SimulationID
			if h.Reports != nil {
				if rep := h.Reports.GetReportBySimulation(simID); rep != nil {
					if !h.Reports.DeleteReport(rep.ReportID) {
						h.Logger.Warn("purge: no se pudo borrar el informe", "report", rep.ReportID)
					}
				}
			}
			if h.OasisStore != nil {
				if err := h.OasisStore.PurgeSimulation(simID); err != nil {
					h.Logger.Warn("purge: no se pudieron borrar los datos sociales", "sim", simID, "error", err)
				}
			}
			if !h.Sim.DeleteSimulation(simID) {
				h.Logger.Warn("purge: no se pudo borrar el directorio de simulación", "sim", simID)
			}
		}
	}

	// 2) Grafo (las FK con ON DELETE CASCADE limpian nodes/edges/episodes/batches).
	if graphID := deref(project.GraphID); graphID != "" {
		gl := graphLock(graphID)
		gl.Lock()
		defer gl.Unlock()
		if err := h.deleteGraphIfPresent(graphID); err != nil {
			h.Logger.Warn("purge: no se pudo borrar el grafo", "graph", graphID, "error", err)
		}
		delGraphLock(graphID)
	}

	// 3) Proyecto y archivos (project.json + uploads).
	if !h.PM.DeleteProject(projectID) {
		return errors.New("no se pudo borrar el proyecto")
	}
	return nil
}
