# Persistencia local e intentos durables

Implementación de `LEASE-003`, 22/10/2026. Es infraestructura del broker;
no implementa políticas, aprobación, concesión ni revocación reales.
`grant.Next` y `reconcile.Diff` siguen como ejercicios del dueño.

## Uso explícito

```sh
go build -o /tmp/lease ./cmd/lease
lease_lab_dir="$(mktemp -d /tmp/lease-lab.XXXXXX)"
/tmp/lease db status --db "$lease_lab_dir/state.sqlite"
/tmp/lease db up --db "$lease_lab_dir/state.sqlite"
/tmp/lease db verify --db "$lease_lab_dir/state.sqlite"
```

`status` no crea una base ausente. `up` es la única acción que inicializa
el esquema. `store.Open` no migra automáticamente: exige una base existente,
esquema reconocido y auditoría consistente. El archivo nuevo tiene permisos
`0600`; la ruta no tiene valor implícito y los symlinks se rechazan.
Los comandos de acceso de `LEASE-005` todavía no están implementados.

La migración SQL se incorpora con `go:embed` y queda registrada con SHA-256.
No se modifican migraciones aplicadas. Una base ajena, una versión desconocida
o un checksum diferente se rechazan; no se adopta ni se repara el archivo.
`status` comprueba versión/historia de migración; `verify` comprueba también
la consistencia de concesiones y auditoría. Ninguno demuestra acceso real.

## Modelo y transacciones

- `entitlements`: proveedor, destino, rol y TTL máximo. Alta inmutable; un
  cambio de configuración usa otro ID. Aún no hay administración de políticas.
- `grants`: solicitud, principal, motivo, ventana de acceso explícita,
  snapshot del entitlement, estado, versión y referencia del proveedor.
- `events`: snapshot de cada alta/transición, actor e instante aportados por
  quien llama, con hash del evento anterior.
- `chain_head`: última secuencia/hash, en la misma transacción que el evento.

Crear una solicitud sólo produce `requested`. No autoriza acceso. Su ventana
absoluta se aporta explícitamente y debe estar vigente al crearla y respetar
el TTL máximo; no se prolonga automáticamente al esperar aprobación. El futuro
servicio debe definir su política de ventanas antes de exponer solicitudes.

`Advance` consulta la máquina del dueño, comprueba la versión esperada y
persiste estado más evento en una transacción. Sólo devuelve éxito después
del commit. El futuro ejecutor debe llamar al proveedor **después** de ese
éxito. No hay un ejecutor implementado aquí ni una llamada real a proveedores.
Un error al escribir evidencia revierte también el estado. Si el commit falla
o su resultado es incierto, no se debe conceder acceso; se recupera el intento.

Antes de crear/avanzar una solicitud, leerla o recuperarla se verifica la cadena
en la misma transacción. Una corrupción posterior a abrir el Store también
impide operar. En este MVP se recorre la evidencia completa: tiene coste lineal
y debe medirse antes de aumentar volumen; no se reemplaza por una confianza
implícita en la cabeza del log.

La referencia del proveedor puede guardarse antes de actuar si se conoce;
una vez guardada no se sustituye por otra. Una concesión incierta sin referencia
necesita resolverse por GrantID/observación: no se puede declarar revocada
sólo por faltar el handle.

Dos escritores con la misma versión no pueden avanzar ambos. SQLite abre
transacciones `IMMEDIATE`, espera locks hasta cinco segundos y usa una conexión
por instancia de Store. Los pragmas `foreign_keys`, `busy_timeout` y
`synchronous=FULL` se aplican por conexión a través del DSN. Se conserva el
journal local predeterminado; no es un diseño para archivos compartidos por red
ni un broker distribuido. No se prueban cortes físicos de energía.

## Recuperación y evidencia

`Recoverable` verifica primero la evidencia dentro de la misma transacción
y devuelve **todos** los estados no terminales. Incluye `granting`, `revoking`
y concesiones vencidas. No supone que un intento quedó sin aplicar, no vuelve
a conceder acceso y no ejecuta `reconcile.Diff` por el dueño.

Cada evento contiene secuencia, actor, instante y snapshot del grant. SHA-256
usa un prefijo de formato y enlaza el hash previo. La verificación comprueba
secuencia, hashes, cabeza, versiones por grant, campos inmutables y coincidencia
del último snapshot con la tabla. Triggers impiden UPDATE/DELETE ordinarios
de eventos. Datos, solicitudes o eventos sin correspondencia fallan cerrados.

Es una comprobación local de consistencia, **no una prueba externa**: alguien
con control de todo el archivo puede reescribirlo y recalcular los hashes.
No hay firma, ancla externa ni garantía frente a un administrador malicioso.
No se almacenan contraseñas o tokens; `Ref` es un identificador de objeto.

## Verificación y aprendizaje

```sh
go test -race -count=1 ./internal/store ./cmd/lease
scripts/verify.sh
```

Las pruebas específicas cubren migración explícita/idempotente, esquema ajeno
o alterado, TTL y límite exacto, alta duplicada, rollback ante fallo de evidencia,
CAS entre conexiones, cadena global concurrente, corrupción de evidencia y
recuperación de cada estado no terminal. Hay subprocesos que terminan sin
Close ni defers antes y después del commit, sobre bases descartables.

Los tests de almacenamiento inyectan resultados del núcleo mediante un campo
privado. No implementan la tabla de transiciones. El constructor público
siempre conecta `grant.Next`: mientras éste siga vacío, las transiciones de
producción se rechazan. Hay una prueba que contrasta explícitamente esa unión.
El gate completo sigue rojo por `LEASE-001`/`LEASE-002`, como exige AGENTS.md;
no se omiten ni se marcan skipped esos ejercicios.

Idiomas Go usados: `defer tx.Rollback()` cubre retornos tempranos; interfaces
pequeñas permiten compartir lectura de DB/Tx; `errors.Is` conserva errores
reconocibles; `context.Context` permite cancelar esperas; los relojes se aportan
como argumento. Los paquetes puros no leen reloj ni base de datos.

Fuentes: [transacciones SQLite](https://sqlite.org/lang_transaction.html),
[pragmas](https://sqlite.org/pragma.html) y [driver fijado modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite@v1.60.1).
