// settings_sqlite.go — variante SQLite personal (binario con -tags
// mirofish_sqlite + server.RunPersonalSQLite):
//
//   - La configuración LLM (LLM_API_KEY / LLM_BASE_URL / LLM_MODEL_NAME) ya
//     no vive solo en el .env: se guarda en la tabla `settings` de SQLite y
//     se edita desde la propia app con el botón ⚙ Config (modal).
//   - Al arrancar se siembra lo que haya en el .env (primera ejecución) y se
//     aplica como override en runtime; cada config.Load() posterior (todas
//     las fábricas de clientes LLM) usa los valores de la BD.
//   - Se inyecta un <script> en el index.html de la SPA: redirige "/" a /app
//     (sin la web de presentación con planes) y añade el botón ⚙ Config con
//     su modal (sin tocar el build original del frontend).
package httpx

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mirofish/internal/config"
	"mirofish/internal/graph"
	"mirofish/internal/webui"
)

const settingsDefaultBaseURL = "https://api.openai.com/v1"
const settingsDefaultModel = "gpt-4o-mini"

// Claves de la tabla settings (nombres de las variables del .env original).
const (
	settingsKeyAPIKey  = "LLM_API_KEY"
	settingsKeyBaseURL = "LLM_BASE_URL"
	settingsKeyModel   = "LLM_MODEL_NAME"
)

// registerSQLiteSettingsRoutes monta el almacén de ajustes y /api/config;
// devuelve false si no se pudo preparar (la app seguirá sin ajustes editables).
func registerSQLiteSettingsRoutes(mux *http.ServeMux, client *graph.Client, cfg *config.Config, logger *slog.Logger) bool {
	if client == nil || client.DB == nil {
		return false
	}
	db := client.DB.SQL()

	if err := settingsEnsureTable(db); err != nil {
		logger.Error("settings: no se pudo crear la tabla settings", "error", err)
		return false
	}

	values, err := settingsReadAll(db)
	if err != nil {
		logger.Error("settings: no se pudo leer settings", "error", err)
		return false
	}

	// Siembra inicial: lo que haya en el .env la primera vez que arranca.
	if values[settingsKeyAPIKey] == "" && cfg.LLMAPIKey != "" {
		seed := map[string]string{
			settingsKeyAPIKey:  cfg.LLMAPIKey,
			settingsKeyBaseURL: orDefault(cfg.LLMBaseURL, settingsDefaultBaseURL),
			settingsKeyModel:   orDefault(cfg.LLMModel, settingsDefaultModel),
		}
		if err := settingsWriteAll(db, seed); err != nil {
			logger.Error("settings: no se pudo sembrar desde .env", "error", err)
		}
		values, _ = settingsReadAll(db)
	}

	// La BD manda sobre .env/entorno a partir de ahora.
	applySettingsOverrides(values)

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		v, err := settingsReadAll(db)
		if err != nil {
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "no se pudo leer la configuración"})
			return
		}
		JSON(w, http.StatusOK, map[string]any{"success": true, "data": settingsPublicDTO(v)})
	})

	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LLMAPIKey  string `json:"llm_api_key"`
			LLMBaseURL string `json:"llm_base_url"`
			LLMModel   string `json:"llm_model"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			JSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "cuerpo ilegible"})
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			JSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "JSON inválido"})
			return
		}
		newValues := map[string]string{
			settingsKeyAPIKey:  strings.TrimSpace(body.LLMAPIKey),
			settingsKeyBaseURL: orDefault(strings.TrimSpace(body.LLMBaseURL), settingsDefaultBaseURL),
			settingsKeyModel:   strings.TrimSpace(body.LLMModel),
		}
		if err := settingsWriteAll(db, newValues); err != nil {
			JSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "no se pudo guardar en SQLite"})
			return
		}
		applySettingsOverrides(newValues)
		JSON(w, http.StatusOK, map[string]any{"success": true, "data": settingsPublicDTO(newValues)})
	})

	return true
}

// applySettingsOverrides consolida en config los ajustes guardados.
func applySettingsOverrides(values map[string]string) {
	config.SetRuntimeSettings(values)
}

func settingsPublicDTO(values map[string]string) map[string]string {
	return map[string]string{
		"llm_api_key":  values[settingsKeyAPIKey],
		"llm_base_url": orDefault(values[settingsKeyBaseURL], settingsDefaultBaseURL),
		"llm_model":    values[settingsKeyModel],
	}
}

// ===== helpers de la tabla settings =====

func settingsEnsureTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

func settingsReadAll(db *sql.DB) (map[string]string, error) {
	out := map[string]string{}
	rows, err := db.Query("SELECT key, value FROM settings")
	if err != nil {
		if createErr := settingsEnsureTable(db); createErr == nil {
			rows, err = db.Query("SELECT key, value FROM settings")
		}
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func settingsWriteAll(db *sql.DB, values map[string]string) error {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	for k, v := range values {
		if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			k, v, now); err != nil {
			return fmt.Errorf("settings %s: %w", k, err)
		}
	}
	return nil
}

// activateSQLiteFrontend adapta la SPA servida por webui.Handler: salto
// directo a /app y botón ⚙ Config. Se llama una vez al montar el router
// personal SQLite.
func activateSQLiteFrontend() {
	webui.SetExtraBodyHTML(settingsFrontendScript)
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// settingsFrontendScript — vanilla JS inyectado en el index.html (no toca el
// build del frontend). Dos cosas:
//  1. "/" redirige a /app: sin la web de presentación con planes. Siembra
//     mirofish_access_key en localStorage (el guard de la SPA exige esa
//     variable para entrar a /app; el backend la ignora si no hay ACCESS_KEY
//     en el .env). Con ACCESS_KEY definida, la validación real falla y la
//     SPA vuelve a su pantalla de clave.
//  2. Botón flotante ⚙ Config con modal que lee/guarda la configuración LLM
//     en SQLite (GET/PUT /api/config con la X-Access-Key de localStorage,
//     igual que hace el frontend con axios).
const settingsFrontendScript = `<script>
(function(){
  try{
    // El guard de la SPA exige mirofish_access_key (aunque el backend no la
    // exija): se siembra una vez; /api/auth/validate responde 200 sin clave.
    if (!localStorage.getItem('mirofish_access_key')) {
      localStorage.setItem('mirofish_access_key', 'personal');
    }
    if (location.pathname === '/' || location.pathname === '') {
      location.replace('/app');
      return;
    }
  }catch(e){}

  var LS = {
    get: function(k){ try{ return localStorage.getItem(k) || ''; }catch(e){ return ''; } }
  };
  var isEN = (LS.get('locale_v2') || 'es').indexOf('en') === 0;
  var T = {
    config: isEN ? 'Config' : 'Config',
    title: isEN ? 'LLM configuration (stored in local SQLite)' : 'Configuración LLM (guardada en SQLite local)',
    key: isEN ? 'LLM_API_KEY' : 'LLM_API_KEY',
    url: isEN ? 'LLM_BASE_URL' : 'LLM_BASE_URL',
    model: isEN ? 'LLM_MODEL_NAME' : 'LLM_MODEL_NAME',
    save: isEN ? 'Save' : 'Guardar',
    saving: isEN ? 'Saving…' : 'Guardando…',
    close: isEN ? 'Close' : 'Cerrar',
    saved: isEN ? 'Configuration saved' : 'Configuración guardada',
    loadErr: isEN ? 'Could not load the configuration' : 'No se pudo cargar la configuración',
    saveErr: isEN ? 'Could not save the configuration' : 'No se pudo guardar la configuración'
  };

  function headers(){
    return { 'Content-Type': 'application/json', 'X-Access-Key': LS.get('mirofish_access_key') };
  }
  function api(url, method, body){
    return fetch('/api/config', { method: method, headers: headers(), body: body ? JSON.stringify(body) : undefined });
  }

  var style = document.createElement('style');
  style.textContent = [
    '#mfz-config-btn{position:fixed;bottom:16px;right:16px;z-index:99998;display:flex;align-items:center;gap:6px;',
      'padding:8px 14px;border-radius:999px;border:1px solid rgba(127,127,127,.45);cursor:pointer;',
      'font:inherit;font-size:14px;background:linear-gradient(180deg,transparent,rgba(127,127,127,.08));color:inherit;}',
    '#mfz-config-btn:hover{filter:brightness(1.15);}',
    '#mfz-back{position:fixed;inset:0;z-index:99999;background:rgba(0,0,0,.55);display:flex;align-items:center;justify-content:center;padding:20px;}',
    '#mfz-modal{width:min(460px,92vw);max-height:86vh;overflow:auto;border-radius:14px;padding:20px;border:1px solid rgba(127,127,127,.35);',
      'background:var(--bg,#14161c);color:inherit;box-shadow:0 18px 60px rgba(0,0,0,.45);font:inherit;}',
    '#mfz-modal h3{margin:0 0 14px;font-size:16px;}',
    '#mfz-modal label{display:block;font-size:12.5px;opacity:.8;margin:12px 0 4px;}',
    '#mfz-modal input{width:100%;box-sizing:border-box;padding:9px 11px;border-radius:9px;border:1px solid rgba(127,127,127,.4);',
      'background:transparent;color:inherit;font:inherit;font-size:13.5px;}',
    '#mfz-modal .mfz-actions{display:flex;gap:10px;justify-content:flex-end;margin-top:18px;}',
    '#mfz-modal button{padding:9px 16px;border-radius:9px;border:1px solid rgba(127,127,127,.4);cursor:pointer;background:transparent;color:inherit;font:inherit;font-size:13.5px;}',
    '#mfz-save{font-weight:600;}',
    '#mfz-msg{font-size:12.5px;margin-top:10px;min-height:16px;}',
    '#mfz-msg.ok{color:#4ade80;}#mfz-msg.err{color:#f87171;}'
  ].join('');
  document.head.appendChild(style);

  var btn = document.createElement('button');
  btn.id = 'mfz-config-btn';
  btn.type = 'button';
  btn.innerHTML = '&#9881; ' + T.config;
  btn.addEventListener('click', function(){ open(); });
  document.body.appendChild(btn);

  function open(){
    var back = document.createElement('div');
    back.id = 'mfz-back';
    var modal = document.createElement('div');
    modal.id = 'mfz-modal';
    modal.innerHTML =
      '<h3>' + T.title + '</h3>' +
      '<label>' + T.key + '</label><input id="mfz-key" type="password" autocomplete="off">' +
      '<label>' + T.url + '</label><input id="mfz-url" type="text" autocomplete="off">' +
      '<label>' + T.model + '</label><input id="mfz-model" type="text" autocomplete="off">' +
      '<div class="mfz-actions"><button id="mfz-close" type="button">' + T.close + '</button>' +
      '<button id="mfz-save" type="button">' + T.save + '</button></div>' +
      '<div id="mfz-msg"></div>';
    back.appendChild(modal);
    back.addEventListener('mousedown', function(e){ if (e.target === back) back.remove(); });
    modal.querySelector('#mfz-close').addEventListener('click', function(){ back.remove(); });
    document.body.appendChild(back);

    var msg = modal.querySelector('#mfz-msg');
    api('/api/config', 'GET').then(function(r){ return r.json(); }).then(function(res){
      var d = (res && res.data) || {};
      modal.querySelector('#mfz-key').value = d.llm_api_key || '';
      modal.querySelector('#mfz-url').value = d.llm_base_url || '';
      modal.querySelector('#mfz-model').value = d.llm_model || '';
    }).catch(function(){ msg.textContent = T.loadErr; msg.className = 'err'; });

    modal.querySelector('#mfz-save').addEventListener('click', function(){
      var saveBtn = modal.querySelector('#mfz-save');
      saveBtn.disabled = true; saveBtn.textContent = T.saving; msg.textContent = ''; msg.className = '';
      api('/api/config', 'PUT', {
        llm_api_key: modal.querySelector('#mfz-key').value,
        llm_base_url: modal.querySelector('#mfz-url').value,
        llm_model: modal.querySelector('#mfz-model').value
      }).then(function(r){ return r.json().then(function(j){ return { ok: r.ok, json: j }; }); }).then(function(res){
        if (res.ok && res.json && res.json.success){ msg.textContent = T.saved; msg.className = 'ok'; }
        else { msg.textContent = T.saveErr; msg.className = 'err'; }
      }).catch(function(){ msg.textContent = T.saveErr; msg.className = 'err'; })
      .then(function(){
        saveBtn.disabled = false; saveBtn.textContent = T.save;
      });
    });
  }
})();
</script>`
