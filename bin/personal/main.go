// MiroFish Personal SQLite — binario de uso personal en Windows sin MySQL:
//
//   - Todo lo persistente (grafos, episodios, simulaciones OASIS y la
//     configuración LLM) vive en un único fichero SQLite:
//     <carpeta del .env>/mirofish.db (o MIROFISH_SQLITE_PATH).
//   - Sin cuenta ni panel admin: acceso por ACCESS_KEY (del .env), como el
//     resto del modo personal.
//   - Sin la web de presentación con planes: "/" entra directo a /app.
//   - Botón ⚙ Config en la app: modal para ver/editar LLM_API_KEY,
//     LLM_BASE_URL y LLM_MODEL_NAME, guardados en SQLite y aplicados al
//     momento (sin reiniciar).
//
// Requiere compilarse con:  go build -tags mirofish_sqlite ./bin/personal
// (ver bin/personal/build.bat). Los binarios MySQL del repo no cambian.
package main

import "mirofish/internal/server"

func main() {
	server.RunPersonalSQLite()
}
