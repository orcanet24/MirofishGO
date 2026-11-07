// Package timex — fechas con el mismo formato que datetime.isoformat().
//
// El backend Python escribe created_at/updated_at con isoformat(): siempre
// segundos, y microsegundos de 6 dígitos SOLO cuando no son cero. El layout de
// Go "2006-01-02T15:04:05.999999" recorta los ceros finales (produce ".123"),
// lo que hacía que un timestamp escrito por el binario Go no coincidiera con el
// formato de Python y rompiera comparaciones/ordenaciones léxicas entre ambos.
package timex

import (
	"fmt"
	"time"
)

// ISO formatea t igual que datetime.isoformat().
func ISO(t time.Time) string {
	base := t.Format("2006-01-02T15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		return fmt.Sprintf("%s.%06d", base, us)
	}
	return base
}

// Now devuelve el instante actual en formato isoformat.
func Now() string { return ISO(time.Now()) }
