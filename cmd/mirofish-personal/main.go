// MiroFish Personal — binario de uso personal, como el original:
// acceso por ACCESS_KEY (la del .env), todos los datos, sin cuentas de
// usuario ni panel admin; usa las credenciales LLM globales del .env.
package main

import "mirofish/internal/server"

func main() {
	server.Run(true)
}
