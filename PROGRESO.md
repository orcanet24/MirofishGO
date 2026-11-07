# Progreso del port MiroFish → binario único Go

Condición única de acceso: **clave maestra `ACCESS_KEY`** en `.env` (middleware
`internal/authx`, comparación constant-time; `X-Access-Key`, `Authorization:
Bearer` o `?access_key=`; `/health` y `/ready` públicos).

Estado verificado el 2026-11-07: `go build ./...`, `go vet ./...`, `go test ./...`
y `go test -race ./internal/{oasis,simx,graph,httpx}` pasan contra **MySQL 8
real**. Prueba de humo del binario: `/health` 200, `/ready` 200, `/api/*` 401
sin clave, 200 con clave, `/api/graph/project/list` y `/api/report/list` 200
leyendo MySQL y los archivos existentes.

`GET /ready` es la readiness de arranque: hace ping a MySQL y comprueba que
existan las tablas del grafo y de OASIS. Responde 200 con
`{"status":"ready","checks":{"database":"ok","schema":"ok"}}`; si falla la
conexión o falta alguna tabla responde 503 con `status: not_ready` y, cuando
corresponde, `checks.missing_tables`. No exige `ACCESS_KEY` y no expone el DSN.

## Cifras

- Rutas del backend Python: **59** (10 graph + 31 simulation + 18 report).
- Portadas y probadas: **54** (10 graph + 26 simulation + 18 report).
- Pendientes: **5** simulation (`interview`, `interview/batch`, `interview/all`,
  `interview/history`, `close-env`) más Reddit, el frontend embebido y el
  aislamiento multi-instancia del runner.
- El runner OASIS nativo ya ejecuta **Twitter** de punta a punta: `start`,
  `stop`, `run-status`, `run-status/detail`, `actions`, `timeline`,
  `agent-stats`, `posts` y `comments` funcionan. Las rutas aún no portadas
  responden **501 explícito** (no datos falsos ni 500 opacos).

## Base de datos (MySQL 8 / MariaDB)

El grafo y su estado viven en MySQL; no hay fallback a SQLite. Configura en el
`.env` de la raíz una de estas dos formas:

```ini
# Opción A: DSN completo (recomendado si la contraseña tiene caracteres raros)
DB_DSN=usuario:clave@tcp(127.0.0.1:3306)/mirofish?charset=utf8mb4&collation=utf8mb4_bin&parseTime=false

# Opción B: variables sueltas
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=mirofish
DB_PASSWORD=...
DB_NAME=mirofish
```

La base debe existir (`CREATE DATABASE mirofish CHARACTER SET utf8mb4`); las
tablas, índices y FULLTEXT se crean al arrancar. El usuario de la aplicación
no necesita `DROP`: con `SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX,
REFERENCES` sobre esa base alcanza para el esquema y las migraciones. No se le
dan privilegios globales ni acceso a otras bases.

**Alcance de esta migración:** el grafo y, desde el port de OASIS, el estado
social de cada simulación (`oasis_users`, `oasis_posts`, `oasis_comments`,
`oasis_follows`, `oasis_likes`, `oasis_traces`, `oasis_rec`, `oasis_actions`,
`oasis_runs`).
Ambos comparten el mismo pool. Los `project.json`, los estados de simulación y
los informes siguen en archivos para no romper la convivencia con el backend
Python. Pasarlos a MySQL (con `tenant_id`) es parte del siguiente hito SaaS.

Pruebas de integración: usan una base efímera por test. Por defecto apuntan a
`mirofish:mirofish@tcp(127.0.0.1:3307)/mirofish_test` (instancia aislada de
desarrollo); se puede cambiar con `MIROFISH_TEST_DSN`. Sin servidor MySQL las
pruebas se **saltan** con un mensaje, no pasan fingiendo.

Instancia aislada usada para verificar este hito (binarios de Laragon, sin
tocar el MySQL de 3306):

```bash
BASE='C:/laragon/bin/mysql/mysql-8.0.30-winx64'
DATA='C:/Proyectos/Python/MiroFish/mirofish-go/.tmp/mysql-test'
"$BASE/bin/mysqld.exe" --no-defaults --initialize-insecure --basedir="$BASE" --datadir="$DATA"
"$BASE/bin/mysqld.exe" --no-defaults --basedir="$BASE" --datadir="$DATA" \
  --port=3307 --bind-address=127.0.0.1 --mysqlx=0 --skip-log-bin

# una sola vez: base y usuario de pruebas (el segundo GRANT permite las bases
# efímeras que crea cada test)
"$BASE/bin/mysql.exe" -h 127.0.0.1 -P 3307 -u root -e "
  CREATE DATABASE IF NOT EXISTS mirofish_test CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
  CREATE USER IF NOT EXISTS 'mirofish'@'%' IDENTIFIED BY 'mirofish';
  GRANT ALL PRIVILEGES ON \`mirofish\_test%\`.* TO 'mirofish'@'%';
  FLUSH PRIVILEGES;"
```

## Motor OASIS (rebanada Twitter)

`internal/oasis` es un port nativo de `clock`, `environment`, `social_agent` y
`social_platform` para Twitter:

- **Reloj**: `clock.go` (time_step para Twitter, `time_transfer` para Reddit).
- **Almacén**: `store.go` crea y consulta `oasis_*` en el mismo MySQL del grafo,
  siempre filtrado por `simulation_id`.
- **Plataforma**: `platform.go` implementa signup, create_post, like, repost,
  quote, follow, refresh y update_rec_table con las reglas del Python (repost y
  quote apuntan al post raíz, like/follow no se repiten, contadores atómicos).
- **Agente**: `agent.go` arma el prompt de entorno (seguidores, seguidos,
  posts), pide UNA acción JSON al LLM y la valida antes de ejecutarla.
- **Runner**: `runner.go` resetea lo social, registra perfiles, publica los
  `initial_posts`, elige agentes activos por hora/actividad y ejecuta las rondas
  con un semáforo de 8 llamadas LLM. Persiste `run_state` en MySQL **y** en
  `run_state.json` (contrato de `/api/report/generate`).
- **Engine**: `engine.go` evita dos corridas del mismo `simulation_id`, permite
  `force` y expone start/stop/state/actions al blueprint.

Acciones disponibles para el LLM en esta rebanada: `create_post`, `like_post`,
`repost`, `quote_post`, `follow`, `do_nothing`. Las demás (comentarios, grupos,
productos, entrevistas) devuelven error explícito y no se ofrecen en el prompt.

La recomendación es **determinista** (likes/shares + recencia) porque el modelo
`twhin-bert` no está portado; el contrato de `refresh` se conserva.

Límites conocidos:

- `platform=parallel` con Reddit habilitado ejecuta **solo Twitter** y devuelve
  `warning` + `platform_effective: "twitter"`; no finge una corrida doble.
- El registro de runners vive en el proceso: con varias réplicas del binario
  sobre la misma BD hace falta coordinación externa (lease/lock) antes de
  escalar. En una sola instancia, `force` recupera un run huérfano.
- No se escriben `twitter/actions.jsonl`: las acciones viven en
  `oasis_actions` y se sirven por `/actions`. Si una herramienta Python las lee
  del disco, hay que añadir el espejo JSONL.
- El agente usa un protocolo JSON (`{"action","args"}`) en vez del
  function-calling de CAMEL: sin CAMEL no hay tool calling, pero las acciones y
  sus reglas son las mismas y se validan antes de ejecutarlas.
- `update_rec_table` recorre todos los posts de la simulación en cada ronda
  (O(posts)); para simulaciones de miles de agentes habrá que hacerlo
  incremental o cachear el ranking.

## Hitos

| Hito | Contenido | Estado |
|---|---|---|
| 0 | config, logx, locale, retry | ✅ |
| 1 | servidor HTTP + CORS + request-logging + clave única | ✅ probado en caliente |
| 2 | models: Project/ProjectManager/Task/TaskManager + tests de compatibilidad JSON | ✅ |
| 3 | utils: llm_client + openai_chat_compat + file_parser + text_processor | ✅ |
| 4a | local_graph MySQL (internal/graph) + ontology_generator | ✅ |
| 4b | graph API (10 rutas) | ✅ integración upload→build→data→delete |
| 5a | simx: entity reader + profile generator + config generator + manager + IPC | ✅ |
| 5b | **simulation API: 16 rutas** (entidades, create, prepare, status, get, list, history, profiles, config, descargas, generate-profiles) | ✅ |
| 5c | runner Twitter: start/stop/run-status/detail/actions/timeline/agent-stats/posts/comments/env-status | ✅ (Reddit, interview y close-env pendientes) |
| 6 | report API (18 rutas) + report_agent (tools + streaming) | ✅ integración generación→capítulos→logs→chat |
| 7 | OASIS nativo: clock, entorno, agente y plataforma Twitter | ✅ rebanada ejecutable; Reddit/recsys embeddings pendientes |
| 8 | frontend Vite con `go:embed` + login por clave | ⬜ |
| 9 | binario único final: cross-compile win/linux/mac | ⬜ (build local y `build_bin.bat` OK; falta cross-compile) |

## Rutas portadas

### /api/graph (10/10)

- [x] GET /project/<project_id>
- [x] GET /project/list
- [x] DELETE /project/<project_id>
- [x] POST /project/<project_id>/reset
- [x] POST /ontology/generate
- [x] POST /build
- [x] GET /task/<task_id>
- [x] GET /tasks
- [x] GET /data/<graph_id>
- [x] DELETE /delete/<graph_id>

### /api/simulation (22/31)

- [x] GET /entities/<graph_id>
- [x] GET /entities/<graph_id>/<entity_uuid>
- [x] GET /entities/<graph_id>/by-type/<entity_type>
- [x] POST /create
- [x] POST /prepare
- [x] POST /prepare/status
- [x] GET /<simulation_id>
- [x] GET /list
- [x] GET /history
- [x] GET /<simulation_id>/profiles
- [x] GET /<simulation_id>/profiles/realtime
- [x] GET /<simulation_id>/config/realtime
- [x] GET /<simulation_id>/config
- [x] GET /<simulation_id>/config/download
- [x] GET /script/<script_name>/download
- [x] POST /generate-profiles
- [x] POST /start — runner Twitter sobre MySQL (`platform=twitter`; `parallel` avisa que Reddit no corre)
- [x] POST /stop — cierre al final de la ronda en curso
- [x] GET /<simulation_id>/run-status — idle 200 si nunca se ejecutó
- [x] GET /<simulation_id>/run-status/detail — estado + acciones
- [x] GET /<simulation_id>/actions — filtros `platform`, `agent_id`, `round_num`
- [x] GET /<simulation_id>/timeline — agrega `oasis_actions` por ronda (filtros `start_round`/`end_round`)
- [x] GET /<simulation_id>/agent-stats — agrega por agente, ordenado por actividad
- [x] GET /<simulation_id>/posts — `oasis_posts` paginado, del más nuevo al más viejo
- [x] GET /<simulation_id>/comments — `oasis_comments`, filtro `post_id`; `create_comment` ya es una acción del agente
- [ ] POST /interview — requiere IPC con el entorno OASIS
- [ ] POST /interview/batch — requiere IPC
- [ ] POST /interview/all — requiere IPC
- [ ] POST /interview/history — requiere IPC
- [x] POST /env-status — responde si el runner sigue vivo en esta instancia
- [ ] POST /env-close — requiere IPC

### /api/report (18/18)

- [x] POST /generate — tarea asíncrona `report_generate` + `task_id`
- [x] POST /generate/status — polling del task (o atajo `simulation_id`)
- [x] GET /<report_id>
- [x] GET /by-simulation/<simulation_id>
- [x] GET /list
- [x] GET /<report_id>/download
- [x] DELETE /<report_id>
- [x] POST /chat — agente sobre el informe con herramientas
- [x] GET /<report_id>/progress — progreso por polling
- [x] GET /<report_id>/sections — capítulos ya escritos
- [x] GET /<report_id>/section/<index>
- [x] GET /check/<simulation_id> — `interview_unlocked`
- [x] GET /<report_id>/agent-log (+ `?from_line=`)
- [x] GET /<report_id>/agent-log/stream
- [x] GET /<report_id>/console-log (+ `?from_line=`)
- [x] GET /<report_id>/console-log/stream
- [x] POST /tools/search
- [x] POST /tools/statistics

Generación: índice con el LLM (5 capítulos máx.) → bucle ReAct por capítulo
(mín. 3 herramientas, máx. 8, 5 iteraciones, conflicto herramienta/Final Answer
con reintentos) → `section_NN.md` guardado en cuanto termina → `full_report.md`.
`interview_agents` devuelve un mensaje honesto de "no disponible" (requiere el
runner) en vez de inventar entrevistas.

Contrato conservado de Python: `/generate` exige `run_state.json` en estado
terminal (`completed`/`stopped`), proyecto en `graph_completed` y grafo
coincidente. El runner Go publica ese archivo en cada ronda además de guardar
el estado en MySQL, así que una simulación Go habilita el informe sin pasos
manuales.

## Decisiones registradas

- **JSON del proyecto**: campos/orden/snake_case idénticos a `Project.to_dict()`;
  los `project.json` existentes de `backend/uploads/projects/` se leen sin
  migración.
- **Task en memoria** como Python (no persiste); singleton thread-safe.
- **Persistencia**: MySQL 8 / MariaDB con `github.com/go-sql-driver/mysql`.
  `DB_DSN` (o `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME`) en `.env`; el
  binario falla al arrancar si no hay BD. **No queda SQLite en el runtime**.
- **Búsqueda del grafo**: `FULLTEXT` de MySQL (`MATCH … AGAINST` en BOOLEAN
  MODE) con fallback léxico por alcance para tokens cortos, stopwords y CJK.
  El fallback es independiente para nodos y aristas: un alcance con resultados
  ya no oculta que el otro está vacío.
- **Un pool por proceso**: `Router` abre un solo `graph.Client` y lo comparte
  entre los tres blueprints (antes cada handler abría su propio pool).
- **PDF**: PyMuPDF → `github.com/ledongthuc/pdf` (sin OCR ni paginación).
- **Encoding**: `x/net/html/charset` + `x/text`, U+FFFD como `errors='replace'`.
- **LLM HTTP**: `net/http`, timeout 300s, `ProviderError` con status saneado.
- **UploadDir**: `<raíz>/backend/uploads`; el binario Go reutiliza los proyectos
  y la BD de grafo del backend Python.
- **Grafo**: `internal/graph` es la única implementación (MySQL local). El
  binario **no** habla con Zep Cloud; `ZEP_API_KEY` no cambia el backend
  (avisa por log) y `GRAPH_BACKEND=zep` falla en `Validate` con un mensaje
  claro en vez de fingir que usa la nube.
- **Despacho de /api/simulation**: manual sobre un subtree (`GET|POST
  /api/simulation/`) porque las formas de las rutas se solapan; registrarlas
  como patrones separados haría que `http.ServeMux` entrara en pánico por
  patrones conflictivos al arrancar.
- **Prepare asíncrono**: tarea `simulation_prepare` + goroutine con callback de
  progreso ponderado por etapas (0-20-70-90-100) y re-adjunto a la tarea activa
  si ya hay una preparación en curso (evita duplicar gasto de LLM).
- **Locale**: el idioma de la petición se pasa como parámetro a
  `PrepareSimulation` (el manager es compartido; guardarlo en un campo sería
  una carrera entre peticiones en idiomas distintos).
- **OASIS social**: tablas `oasis_*` en el mismo MySQL, aisladas por
  `simulation_id`, con FK y contadores transaccionales. Sin SQLite por corrida:
  el SaaS puede consultar y limpiar una simulación sin abrir archivos.
- **Runner en proceso**: un goroutine por simulación con semáforo de LLM; el
  estado se publica en cada ronda. La UI sigue haciendo polling, no se inventó
  un protocolo nuevo.
- **Recomendación sin embeddings**: ranking determinista de likes/shares y
  recencia; el contrato de refresh (recomendados + seguidos) se mantiene.

## Correcciones de esta pasada

| # | Bug | Efecto | Fix |
|---|---|---|---|
| 1 | `time.Format("...999999")` recorta ceros | timestamps `.123` en vez de `.123000`; rompe comparación con Python | `internal/timex.ISO` replica `isoformat()` |
| 2 | `BatchList`/`BatchListItems` con cursor > len | pánico `slice bounds out of range` con `?cursor=` fuera de rango | `paginateSlice` con clamp |
| 3 | `DB.GetNode` propagaba `sql.ErrNoRows` | `/entities/<g>/<uuid>` desconocido devolvía 500 en vez de 404 | devuelve `(nil, nil)` |
| 4 | `ProjectManager.SaveProject` con `os.WriteFile` | `project.json` truncado visible ante corte/lectura concurrente | escritura atómica (tmp+rename) |
| 5 | `ProjectManager.DeleteProject` sin mutex | podía borrar el directorio durante una escritura | toma el mutex |
| 6 | `simx.LoadSimulationState` devolvía el puntero de caché | data race entre polling HTTP y la goroutine de preparación | caché guarda y devuelve copias |
| 7 | `simx.simulationDir` hacía `MkdirAll` al leer | ids inexistentes creaban carpetas y ensuciaban uploads | `SimulationDir` sin efectos + `Exists` |
| 8 | `_check_simulation_prepared` exigía ambos perfiles | una simulación Twitter-only nunca se consideraba preparada (regeneraba y gastaba LLM) | exige solo los perfiles de plataformas habilitadas |
| 9 | `state.json` escrito con `os.WriteFile` | lectura truncada durante el polling de progreso | escritura atómica |
| 10 | `ContentLength` ignorado en chuncked | subida multipart sin límite real de memoria | `http.MaxBytesReader` + 413 |
| 11 | JSON inválido en `/build` se ignoraba | respondía "falta project_id" como si el cuerpo fuera válido | 400 explícito |
| 12 | Errores devolvían `traceback` con `debug.Stack()` | filtraba rutas internas al cliente | se registra en log, no en la respuesta |
| 13 | Test de extracción en background sin espera | fallo intermitente (300 consultas en <5 ms) | sondeo con `time.Sleep` y deadline |
| 14 | `go.mod` marcaba deps directas como `// indirect` | higiene del módulo | `go mod tidy` |
| 15 | `edges_fts` sin triggers (solo existían para `nodes_fts`) | índice de aristas siempre vacío: búsqueda `both` devolvía nodos y perdía hechos; el report agent recibía contexto vacío | triggers `edges_ai/ad/au`, `nodes_au` y `rebuild` FTS al abrir la BD |
| 16 | `node.summary` se actualizaba sin trigger | `nodes_fts` quedaba obsoleto tras deduplicar entidades | trigger `nodes_au` + rebuild |
| 17 | `stageDetails` escrito desde las goroutines de perfiles | data race y posible `concurrent map writes` al preparar simulaciones en paralelo | mutex en el callback de progreso |
| 18 | Tres handlers abrían tres pools SQLite | tras migrar a MySQL serían hasta 60 conexiones por proceso | `Router` comparte un único `graph.Client` |
| 19 | `quote_post` de un repost leía el contenido del repost (vacío) | citar un repost fallaba con "sin contenido" | se resuelve el post raíz antes de citar |
| 20 | `RunState` sin tags JSON | el run_state guardado en MySQL se releía vacío (status/rounds en cero) | tags `snake_case` en el struct |

## Empaquetado Windows

`build_bin.bat` (raíz del repo) compila el backend con `CGO_ENABLED=0` y
`-trimpath -ldflags "-s -w"` en `bin\mirofish.exe`. El mismo script copia el
`.env` a `bin\.env`, copia los scripts de simulación, crea
`bin\backend\uploads` y `bin\logs`, y genera `bin\start.bat` (hace `cd` a
`bin` para que `BaseDir` sea la carpeta portable). El binario no necesita Go en
el equipo destino. No incluye el frontend embebido (hito 8) ni copia datos de
`backend\uploads`: el `bin` arranca con datos vacíos salvo que se copien.

## Qué falta para "100% Go"

1. **OASIS Reddit y acciones restantes**: likes de comentarios, grupos,
   productos, mute/report, búsqueda y `interview` (IPC). El esquema MySQL ya
   está separado por `simulation_id`; faltan las tablas y reglas de Reddit y la
   recsys con embeddings si se quiere paridad exacta.
2. **Runner completo**: barreras entre plataformas, reanudación desde
   `start_round`, limpieza de logs, PID/stop forzado y espejo
   `twitter/actions.jsonl`. El start/stop básico ya funciona.
3. **Frontend embebido** (`go:embed`): hoy el binario no sirve la UI; el
   `dist/` de Vite vive fuera del módulo Go y hay que copiarlo a `mirofish-go/web`.
4. **Cross-compile** win/linux/mac y verificación con datos reales.
5. **Importador SQLite → MySQL** para los grafos existentes de
   `backend/data/local_graph.db`: la migración no borra ese archivo, pero el
   binario ya no lo lee. Hasta portarlo, los proyectos viejos conservan sus
   `project.json`/informes pero su grafo aparece vacío.
