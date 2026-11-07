# MiroFish — Guía de Administración (SaaS)

Documento para el administrador del sistema: cómo entrar al panel, cómo gestionar
usuarios y planes, y dónde vive cada cosa.

---

## 1. Acceso administrativo

Hay **tres formas** de entrar con acceso total:

| Forma | Cuándo usarla | Credencial |
|---|---|---|
| **Cuenta admin** | Uso diario del panel | `admin@mirofish.local` / `secreto123` (cuenta ya creada en tu BD de desarrollo) |
| **Primera cuenta registrada** | Base de datos vacía | La primera cuenta que se registre es admin automáticamente |
| **Clave maestra `ACCESS_KEY`** | Si olvidas la contraseña o la BD de usuarios | La definida en `bin\.env` (ahora mismo **no está definida**) |

> ⚠️ **Importante**: la contraseña `secreto123` es de prueba. Cámbiala en
> **Mi cuenta → Cambiar contraseña** la primera vez.

### Cuenta admin actual (tu BD de desarrollo)

```
Email:    admin@mirofish.local
Password: secreto123
Rol:      admin
Plan:     Emprendedor (4 créditos)
```

---

## 2. Cómo usar el SaaS (flujo completo)

### 2.1. Entrar

1. Abre `http://localhost:5001` (o el puerto que tengas).
2. **Entrar** → email + contraseña.
3. Si eres admin verás en el menú: **Abrir la app**, **Mi cuenta**, **Administración**.

### 2.2. Registro de usuarios

- Los usuarios se registran solos desde **Crear cuenta** (email + contraseña, mínima 6 caracteres).
- También puedes crearlos tú desde **Administración → + Nuevo usuario** (útil para altas manuales).
- Una cuenta nueva entra con **0 créditos** y sin plan: debe esperar a que el admin le asigne uno.

### 2.3. Panel de administración (`/admin`)

Pestaña **Administración** del menú. Tabla con todos los usuarios:

| Columna | Significado |
|---|---|
| Email | Identificador de acceso |
| Rol | `Admin` o `Usuario` |
| Plan | Básico / Emprendedor / Empresa (— = sin plan) |
| Estudios | Créditos disponibles |
| LLM | ✓ si tiene API key propia asignada |
| Activo | ✓ / ✕ (desactivar bloquea el login sin borrar datos) |

Pulsando **Editar** se abre el modal de gestión:

1. **Asignar plan** — eliges Básico / Emprendedor / Empresa. Al asignar un plan los créditos se cargan solos (1 / 4 / 20).
2. **Sumar estudios** — créditos extra (bonus, compensaciones). Acepta negativos para descontar.
3. **Fijar estudios** — pone un valor absoluto.
4. **Nueva contraseña** — reset administrativo (sin conocer la actual).
5. **LLM** — la API key / Base URL / modelo que usará **ese usuario** en todas sus operaciones (ontología, extracción de grafos, simulación, informes, chat).
   - **Vacío = usa la config global del servidor** (la del `.env`).
   - Así es como haces que cada cliente pague/consuma sus propias credenciales de IA.
6. **Rol / Activar-Desactivar** — promover a admin o suspender la cuenta.

Los cambios se guardan con su botón **Guardar** correspondiente.

### 2.4. Planes y precios

| Plan | Estudios | Precio | Por estudio |
|---|---|---|---|
| **Básico** | 1 | $10 | $10,00 |
| **Emprendedor** | 4 | $29 | $7,25 |
| **Empresa** | 20 | $99 | $4,95 |

**Flujo de cobro (manual, por tu cuenta):**

1. El usuario se registra y te escribe para pagar.
2. Tú recibes el pago (transferencia, etc.).
3. En **Administración → Editar** le asignas el plan → se cargan los créditos.
4. El usuario consume **1 crédito por estudio** (se descuenta al completar la ontología de un proyecto; si no tiene créditos recibe error 402).

### 2.5. Aislamiento entre usuarios

- Cada usuario **solo ve sus propios** proyectos, simulaciones e informes. Nadie puede ver ni tocar lo de otro (los permisos se verifican en cada petición).
- Los datos legacy creados antes del multi-usuario (sin `user_id`) solo los ve el admin.
- El admin y la clave maestra ven **todo**.

### 2.6. App del usuario

- **Abrir la app** (`/app`): subir informes (PDF/MD/TXT) + prompt de simulación → construye el grafo → simula → genera el informe.
- **Mi cuenta** (`/account`): ve su plan, sus créditos disponibles, cambia su contraseña.

---

## 3. Binario personal (uso privado)

`bin\mirofish-personal.exe` (o `bin/mirofish-personal-linux`):

- **No tiene cuentas ni panel**: acceso por **una sola clave** (`ACCESS_KEY`).
- Ve **todos** los datos y usa las credenciales LLM del `.env`.
- Para definir la clave, agrégala en `bin\.env`:

  ```env
  ACCESS_KEY=tu_clave_secreta
  ```

- Arranca con `bin\start-personal.bat`.
- La misma clave, si el binario SaaS usa el mismo `.env`, también sirve como **admin maestro** del SaaS.

---

## 4. Dónde vive cada cosa

| Elemento | Ubicación |
|---|---|
| Usuarios, créditos, config LLM por usuario | MySQL, tabla `users` (base `mirofish`) |
| Proyectos y archivos subidos | `<base>\backend\uploads\projects\` (JSON + archivos) |
| Simulaciones | `<base>\backend\uploads\simulations\` |
| Grafo (nodos/edges) | MySQL (`graphs`, `nodes`, `edges`, `episodes`, `batches`) |
| Config del servidor (LLM global, MySQL, ACCESS_KEY, LOG_LEVEL) | `bin\.env` |
| Logs del servidor | `bin\logs\<fecha>.log` (DEBUG+, con componente y peticiones) |
| Binarios | `bin\mirofish.exe`, `bin\mirofish-linux`, `bin\mirofish-personal.exe`, `bin\mirofish-personal-linux` |
| Frontend (código fuente) | `C:\Proyectos\Python\MiroFish\frontend` |
| Frontend (empotrado en el binario) | `internal\webui\dist` |

---

## 5. Operación diaria

```powershell
# Arrancar el SaaS (multi-usuario)
bin\start.bat

# Arrancar el binario personal
bin\start-personal.bat

# Ver logs en vivo (consola ya muestra peticiones INFO; para depurar pon en el .env):
#   LOG_LEVEL=debug
```

**Para recompilar** tras cambiar código: `build_bin.bat` (genera los 4 binarios).
Si cambiaste el frontend: `npm run build` en `frontend\` → copiar `frontend\dist` a
`internal\webui\dist` → recompilar.

---

## 6. Seguridad mínima recomendada

1. Cambia la contraseña de `admin@mirofish.local`.
2. Define `ACCESS_KEY` en `bin\.env` si quieres clave maestra de respaldo.
3. `SECRET_KEY` (firma de tokens) debe ser aleatoria en producción: genera una con
   `openssl rand -hex 32` y ponla en el `.env`.
4. No expongas el puerto 5001 a Internet sin HTTPS por delante (Caddy/nginx).
