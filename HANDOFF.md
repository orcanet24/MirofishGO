# MiroFish — Estado de la sesión (handoff)

> Registro para retomar el trabajo: qué está terminado y verificado, qué está
> escrito pero sin verificar, y qué falta. Última sesión: 2026-11-08.

---

## 0. Ronda del 2026-11-08 (interacción profunda + población + web)

### Implementado y verificado
- **Panel único "Configuración del mundo"** en Step2 (siempre visible al entrar a
  la simulación, con borde naranja): **población** (género, edad, educación,
  estrato, etnia, religión, personas/empresas, región), **profundidad de la
  interacción** (1-5 con su etiqueta de agentes/rondas) e **investigación web**
  (toggle + slider 1-5). Precarga los valores guardados de `state.json` antes de
  auto-arrancar el prepare; se envían con `prepare` (`population`,
  `interaction_depth`, `web_research`). Verificado en la UI.
- **Profundidad de interacción (1-5)**: `oasis.DepthPlan` (5/12/20/35/50 agentes,
  1-5 rondas), `SelectAgents` con puntuación de experto, entrevistas
  multi-ronda con seguimientos del LLM, anti-degeneración. Selector global en la
  barra de acción de Step5 (chat + encuesta). Verificado: depth 1 responde con
  5 agentes coherentes.
- **Población demográfica**: `simx.PopulationSpec` inyectada en los prompts de
  generación de perfiles; atributos nuevos en el CSV (education,
  socioeconomic_status, ethnicity, religion, organization). Guardada en
  `state.json`.
- **Investigación web con control de profundidad**: `internal/webresearch`
  (DuckDuckGo HTML + descarga BFS por niveles de enlace). **Verificado en vivo**:
  5 fuentes y 5 páginas (~25k caracteres de hechos reales de "precios de
  autopartes en Venezuela"). Se guarda en `web_context.json` y se inyecta en la
  ontología y en el agente de informes (contexto + herramienta `web_search`).
  El prepare también puede re ejecutarla (web_research en el body).
- **Link de GitHub eliminado**.

### Pendiente de verificación (mañana)
- **Entrevistas profundas (depth 4-5) son lentas en síncrono**: depth 3 (60
  llamadas LLM) supera el timeout de 5 min. **Mejora pendiente: ejecutarlas de
  forma asíncrona** (task + polling, como los informes).
- Fluxo completo con web research: crear un estudio con el toggle activado y
  comprobar que la ontología/informe incorporan los hechos.
- `PROGRESO.md` desactualizado; `ADMIN.md` falta añadir legal/borrado/PDF/web.
- **Política de usos prohibidos (frente 3 original, NO empezado)**.

---

## 1. Terminado y VERIFICADO (funciona con datos reales)

### Multi-usuario / SaaS
- Cuentas, JWT, roles, panel admin (`/admin`), registro/login, aislamiento total
  entre usuarios (proyectos, simulaciones, reportes, tasks).
- Planes y créditos: `UsersStore` YA inyectado en el blueprint de grafo; 402 sin
  créditos, consumo por estudio (verificado con tests unitarios + smoke e2e).
- API key LLM por usuario: se propaga por ontología, extracción, perfiles,
  simulación, motor OASIS, agente de informes y chat (verificado por contrato).
- `ACCESS_KEY` como clave maestra (binario personal y respaldo admin).
- Tests: `go test ./...` en verde.

### Informe PDF (con gráficos)
- `internal/reportpdf` (gofpdf + gonum/plot): portada, resumen, 5 gráficos
  (acciones por ronda, tipos de acción, top posts, entidades, relaciones) y el
  contenido completo del informe.
- Endpoint `GET /api/report/{id}/pdf` verificado con un informe REAL
  (`report_757d68350cf7.pdf`, 143 KB, descargado desde el navegador).
- Generación de informes con `run_state` obsoleto (proceso muerto) ya no da 409
  (detección de estado "running" de >15 min).

### Entrevistas / chat con agentes
- `POST /api/simulation/interview|interview/batch|interview/all` implementados
  en proceso (`internal/oasis/interview.go`), con anti-degeneración (reintento a
  temperatura baja si hay bucles) y validación real de la clave
  (`/api/auth/validate`).
- Verificado con la simulación real: respuesta coherente en español.

### Auditoría de consistencia (3 pasadas) — correcciones aplicadas
- **Fuga de API keys entre usuarios** (`h.Tools.LLMFactory` compartido) → Tools
  por petición.
- Contratos del informe corregidos: `report_start`, `section_complete.content`,
  `message`/`result_length`, marcadores de herramientas en español (frontend
  actualizado), `action_type` en MAYÚSCULAS + `platform`, QuickSearch con
  aristas/nodos, campos por plataforma (`*_simulated_hours`, `*_completed`).
- `stop` idempotente; `close-env` = cierre elegante; `GetRunInstructions` honesto.
- Limpieza periódica de tasks + liberación de locks/runners.
- Defaults de config aplicados (`REPORT_AGENT_*`, `OASIS_DEFAULT_MAX_ROUNDS`).
- Perfiles de Twitter con profesión/edad/género/país/MBTI.
- `/ready` verifica la tabla `users`; `SECRET_KEY` con aviso; `.env` documentado
  (sin el texto falso de SQLite).
- Higiene git: `bin/` (secretos, uploads) y `*.log` ya NO están trackeados.

### Legal y borrado total (verificados)
- `/legal` con confidencialidad/usos prohibidos/jurisdicción Venezuela+UE;
  checkbox obligatorio en registro (`legal_accepted_at` en BD); registro sin
  aceptar → 400, con aceptar → 200.
- `DELETE /api/graph/project/{id}/purge`, `POST /api/graph/purge-all`,
  `DELETE /api/auth/account`. Verificado con datos sintéticos: eliminó proyecto,
  simulación y filas oasis; el grafo real del usuario quedó intacto.
- Botones en UI: ✕ en tarjetas de historial, "Borrar todos mis estudios" y
  "Eliminar mi cuenta" en Mi cuenta.

---

## 2. Pendiente (por orden sugerido)

1. **Política de usos prohibidos (frente 3 del pedido original, NO empezado)**:
   - Paquete `internal/policy` con texto duro en los prompts de todos los
     sub-agentes (ontología, extracción, perfiles, config, informe, entrevistas,
     chat) + detección de contenido prohibido (abuso de menores, guerra,
     sabotaje a entes gubernamentales, etc.).
   - Aplicar a la entrada del estudio (rechazar 400 si el requisito es
     prohibido) y a posts/comentarios generados (descartar + log).
2. **Verificaciones de la ronda 08** (lista al inicio del todo).
3. **Actualizar `ADMIN.md`** con: legal/checkbox, borrado total, PDF, web.
4. Opcional: `PROGRESO.md` está desactualizado y código muerto no dañino
   (`internal/simx/ipc.go`, `internal/retry`, `GeneratePythonCode` en
   `ontology/validate.go`, `ParseActionJSON`), límites/hardcodes dispersos.

---

## 3. Notas operativas importantes

- **Build con poca RAM**: `go build -p 1` + `$env:GOGC="40"`; si el compilador
  falla con "out of memory", cerrar pestañas/procesos y reintentar.
- **Cuenta admin actual (BD dev)**: `admin@mirofish.local` / `secreto123`.
- **`bin\.env`**: sin `ACCESS_KEY` → modo dev abierto; `SECRET_KEY` vacía →
  aviso (default público). Documentados `LOG_LEVEL`, `REPORT_AGENT_*`, etc.
- **Credenciales en el historial de git**: la `bin\.env` con la API key y el
  `DB_PASSWORD` llegó a estar trackeada (ya se destracked). Considerar rotar
  claves si el repo se comparte.
- **Flujo frontend**: el fuente vive en `C:\Proyectos\Python\MiroFish\frontend`;
  el binario Go embebe `internal\webui\dist`. `build_bin.bat` genera los 4
  binarios (Windows + Linux, normal + personal) pero espera el `frontend\` en la
  raíz de MirofishGO (hoy no existe): el copiado es manual o ajustar el script.
- **Web research**: usa DuckDuckGo HTML (sin clave); si el proveedor bloquea la
  IP, la ontología continúa sin contexto web (avisa en el log).
- **Tests de users**: `MIROFISH_TEST_DIRECT=<dsn>` para usar la BD real (el
  helper efímero requiere CREATE DATABASE, que el usuario `mirofish` no tiene).

### Multi-usuario / SaaS
- Cuentas, JWT, roles, panel admin (`/admin`), registro/login, aislamiento total
  entre usuarios (proyectos, simulaciones, reportes, tasks).
- Planes y créditos: `UsersStore` YA inyectado en el blueprint de grafo; 402 sin
  créditos, consumo por estudio (verificado con tests unitarios + smoke e2e).
- API key LLM por usuario: se propaga por ontología, extracción, perfiles,
  simulación, motor OASIS, agente de informes y chat (verificado por contrato).
- `ACCESS_KEY` como clave maestra (binario personal y respaldo admin).
- Tests: `go test ./...` en verde.

### Informe PDF (con gráficos)
- `internal/reportpdf` (gofpdf + gonum/plot): portada, resumen, 5 gráficos
  (acciones por ronda, tipos de acción, top posts, entidades, relaciones) y el
  contenido completo del informe.
- Endpoint `GET /api/report/{id}/pdf` verificado con un informe REAL
  (`report_757d68350cf7.pdf`, 143 KB, descargado desde el navegador).
- Generación de informes con `run_state` obsoleto (proceso muerto) ya no da 409
  (detección de estado "running" de >15 min).

### Entrevistas / chat con agentes
- `POST /api/simulation/interview|interview/batch|interview/all` implementados
  en proceso (`internal/oasis/interview.go`), con anti-degeneración (reintento a
  temperatura baja si hay bucles) y validación real de la clave
  (`/api/auth/validate`).
- Verificado con la simulación real: respuesta coherente en español.

### Auditoría de consistencia (3 pasadas) — correcciones aplicadas
- **Fuga de API keys entre usuarios** (`h.Tools.LLMFactory` compartido) → Tools
  por petición.
- Contratos del informe corregidos: `report_start`, `section_complete.content`,
  `message`/`result_length`, marcadores de herramientas en español (frontend
  actualizado), `action_type` en MAYÚSCULAS + `platform`, QuickSearch con
  aristas/nodos, campos por plataforma (`*_simulated_hours`, `*_completed`).
- `stop` idempotente; `close-env` = cierre elegante; `GetRunInstructions` honesto.
- Limpieza periódica de tasks + liberación de locks/runners.
- Defaults de config aplicados (`REPORT_AGENT_*`, `OASIS_DEFAULT_MAX_ROUNDS`).
- Perfiles de Twitter con profesión/edad/género/país/MBTI.
- `/ready` verifica la tabla `users`; `SECRET_KEY` con aviso; `.env` documentado
  (sin el texto falso de SQLite).
- Higiene git: `bin/` (secretos, uploads) y `*.log` ya NO están trackeados.

### PDF/legal/purge — estado al cierre
- **Legal**: textos es/en en `locales/*.json` (`legal.*`), `LegalView.vue`,
  ruta `/legal`, checkbox de aceptación en registro y `legal_accepted_at` en BD
  (migración incluida). **VERIFICADO**: la página /legal renderiza, el
  registro sin `legal_accepted` → 400, con aceptación → 200 (y se guardó la
  fecha en BD).
- **Borrado total**: `DELETE /api/graph/project/{id}/purge`,
  `POST /api/graph/purge-all`, `DELETE /api/auth/account`. **VERIFICADO** con
  datos sintéticos: eliminó proyecto + simulación + filas oasis; el grafo real
  del usuario quedó intacto.
- **Build completo reconstruido** con todo lo anterior (frontend + 4 binarios)
  y servidor reiniciado (`/ready` 200).

---

## 2. Pendiente (por orden sugerido)

1. **Política de usos prohibidos (frente 3 del pedido original, NO empezado)**:
   - Paquete `internal/policy` con texto duro en los prompts de todos los
     sub-agentes (ontología, extracción, perfiles, config, informe, entrevistas,
     chat) + detección de contenido prohibido (abuso de menores, guerra,
     sabotaje a entes gubernamentales, etc.).
   - Aplicar a la entrada del estudio (rechazar 400 si el requisito es
     prohibido) y a posts/comentarios generados (descartar + log).
2. **Actualizar `ADMIN.md`** con: legal/checkbox, borrado total, PDF.
3. Opcional: `PROGRESO.md` está desactualizado (lo marcó la auditoría).
4. Opcional (limpieza, marcado por la auditoría): código muerto no dañino
   (`internal/simx/ipc.go`, `internal/retry`, `GeneratePythonCode` en
   `ontology/validate.go`, `ParseActionJSON`), límites/hardcodes dispersos,
   timestamps RFC3339 vs `timex.ISO`.

---

## 3. Notas operativas importantes

- **Build con poca RAM**: `go build -p 1` + `$env:GOGC="40"`; si el compilador
  falla con "out of memory", cerrar pestañas/procesos y reintentar.
- **Cuenta admin actual (BD dev)**: `admin@mirofish.local` / `secreto123`.
- **`bin\.env`**: sin `ACCESS_KEY` → modo dev abierto; `SECRET_KEY` vacía →
  aviso (default público). Documentados `LOG_LEVEL`, `REPORT_AGENT_*`, etc.
- **Credenciales en el historial de git**: la `bin\.env` con la API key y el
  `DB_PASSWORD` llegó a estar trackeada (ya se destracked). Considerar rotar
  claves si el repo se comparte.
- **Flujo frontend**: el fuente vive en `C:\Proyectos\Python\MiroFish\frontend`;
  el binario Go embebe `internal\webui\dist`. `build_bin.bat` genera los 4
  binarios (Windows + Linux, normal + personal) pero espera el `frontend\` en la
  raíz de MirofishGO (hoy no existe): el copiado es manual o ajustar el script.
- **Tests de users**: `MIROFISH_TEST_DIRECT=<dsn>` para usar la BD real (el
  helper efímero requiere CREATE DATABASE, que el usuario `mirofish` no tiene).
