// Package fsx — utilidades de sistema de archivos compartidas por los
// paquetes que persisten estado en disco.
package fsx

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic escribe data en path de forma atómica: crea un temporal en el
// mismo directorio, lo sincroniza, y lo renombra sobre el destino.
//
// Escribir directamente sobre el archivo destino (os.WriteFile) lo trunca
// antes de escribir; cualquier lector concurrente —el polling de state.json del
// frontend, o un reinicio a mitad de escritura— puede observar JSON truncado.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op tras un rename correcto

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
