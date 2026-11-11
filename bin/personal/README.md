# MiroFish Personal SQLite (Windows)

Versión de uso personal en una sola PC: **sin MySQL, sin cuentas, sin web de
presentación con planes**. Es un binario independiente del resto del repo —
los binarios `mirofish.exe` (multi-usuario) y `mirofish-personal.exe` siguen
funcionando igual, contra MySQL.

## Qué usa este binario

- Persistencia completa (grafos, episodios, batches, simulaciones OASIS y la
  configuración LLM) en **un único fichero SQLite**: `mirofish.db`, en esta
  carpeta (o la ruta de `MIROFISH_SQLITE_PATH`).
- Acceso por `ACCESS_KEY` del `.env` (vacío → sin clave, como el resto del
  modo personal).
- La URL `/` redirige directo a `/app`: nada de landing con planes.
- **Botón ⚙ Config** abajo a la derecha de la app: modal para ver/editar
  `LLM_API_KEY`, `LLM_BASE_URL` y `LLM_MODEL_NAME`. Se guardan en la tabla
  `settings` de SQLite y se aplican al momento (sin reiniciar). La primera
  vez se siembran desde el `.env` si allí había `LLM_API_KEY`.

## Uso rápido

1. `build.bat` (desde cualquier sitio: cd a la raíz del repo y compila) →
   genera `bin\personal\mirofish-personal-sqlite.exe`.
   > Nota: es una compilación grande (SQLite puro Go); si el enlazador se
   > queda sin memoria, cierra navegadores pesados y reintenta.
2. `start.bat` → arranca en `http://127.0.0.1:5001` (leer `MIROFISH_HOST/PORT`
   de `.env`; ver `.env.example`).
3. En la app: entra a `/app` y usa ⚙ **Config** para tus credenciales LLM.

## Archivos

- `main.go` — punto de entrada (`server.RunPersonalSQLite()`); requiere
  compilar con `-tags mirofish_sqlite` (enlaza el driver SQLite puro Go
  `modernc.org/sqlite`).
- `build.bat`, `start.bat`, `wait-and-build.ps1`, `.env.example`.
- BD: `mirofish.db` (+ `mirofish.db-wal` / `-shm`, normales en modo WAL).
