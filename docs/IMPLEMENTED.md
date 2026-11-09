# Implemented — Lease

## Scaffold — 2026-10-20

Repository layout, design document, trackers, verification gate, `Provider`
interface and the specification tests for `LEASE-001` and `LEASE-002`. No
functional code yet.

## LEASE-003 — SQLite intent store — 2026-10-22

Historical snapshot of `c45a78e`; current product status is recorded below.

Explicit checksummed migration; immutable entitlement creation; durable request
and versioned transition storage; atomic state/event commits; SHA-256 chained
events and head verification; non-terminal recovery after reopening and abrupt
process exits. No real provider is called. See [storage contract](storage.md).

CLI foundation for LEASE-005: `lease db up/status/verify --db PATH`. It never
initializes a database implicitly on status, verify or normal store opening.
At that storage milestone, the request/approval/revoke CLI was still pending.

At that storage milestone, the state-machine and diff exercises were unchanged. Storage tests use
private doubles to isolate transactions; production always uses `grant.Next`.
This closes storage infrastructure only, not policy or revocation guarantees.

Verification observed on 2026-10-22:

- `go test -race -count=1 ./internal/store ./cmd/lease`: green, including crash,
  concurrent writer, rollback, corruption, handle and expiry-boundary cases.
- `scripts/verify.sh`: gofmt and vet green; the race stage passes storage/CLI
  and fails only `TestNextValid` and `TestDiff` in the two untouched exercises.
  Its later build/diff stages do not run after that expected failure.
- `go build -o /dev/null ./cmd/lease` and `git diff --check`: green separately.
- Built CLI smoke on a disposable `/tmp/lease-lab.*` database: status 0 before
  migration, explicit up to schema 1, audit verification OK, status 1 afterwards;
  the file is mode 0600. These schema versions are not process exit codes.
- Local Markdown links validated. No live provider, production database,
  credential, Custodia checkout or service was used. This snapshot was later committed as `c45a78e`.

## Corte de producto local — 2026-10-22

Autorización explícita del dueño: completar el producto y su web, incluido el
núcleo antes reservado para práctica. El estado anterior quedó en `c45a78e`.
No hay push, publicación ni conexión con Custodia o Portfolio en este corte.

- **LEASE-001:** máquina pura implementada; pruebas originales conservadas.
  Se documentan transiciones adicionales de vencimiento de aprobación, adopción
  de huérfanos y evidencia de reintentos de revocación.
- **LEASE-002:** comparación por Key y GrantID, límite `expires_at == now`,
  grants solapados, ausencia y objetos propios con atributos/membresías rotos.
- **LEASE-004:** primer proveedor PostgreSQL por elevación, NOLOGIN temporal,
  idempotencia, metadata de ownership y mapeos originales, observación acotada,
  cierre de sesiones y revocación recuperable. `NativeExpiry=false`. El modo
  de credencial efímera y el operador restringido quedan pendientes por separado.
- **LEASE-005:** CLI `init/request/approve/deny/revoke/list/events/reconcile/run`,
  además de mantenimiento de DB. Política local, motivo, TTL, sin autoaprobación
  ni autoaprobación del solicitante. `--as` atribuye, no autentica.
- **LEASE-006:** ejecutor y worker, intentos antes de efectos, resultados
  desconocidos a limpieza, reintentos registrados, lock entre procesos, ausencia
  confirmada antes de Revoked, adopción atómica de huérfanos y reporte de faltantes.
- **LEASE-040:** presentación, guía y consola de laboratorio con datos ficticios
  de Litoral, tres vistas, detalle, filtros, roles simulados y exportación de
  evidencia. Web estática exportable; núcleo Go compartido con la CLI. Ver
  [web y procedencia](WEB.md). La API sólo admite lab/loopback/origen propio.

### Evidencia observada

- `scripts/verify.sh`: gate completo verde (gofmt, vet, todos los paquetes con
  race, build y diff). Ya no hay ejercicios deliberadamente rojos.
- `scripts/test-postgres.sh`: PASS con PostgreSQL 18 en clúster descartable,
  sólo socket Unix privado. Acceso antes/después, idempotencia y conflicto,
  observación, vencimiento sin worker, drift de membresía, revocación sin Ref,
  cierre de sesión con SET ROLE, nuevas sesiones sin permiso, revoke repetido,
  rol ajeno preservado y metadata con comillas/backslash bajo
  `standard_conforming_strings=off`.
- `scripts/test-web.sh`: PASS en Chromium. Solicitud, aprobación, revocación,
  vencimiento real de diez segundos, vistas y exportación estática. Sin scroll
  horizontal de consola a 320/375/768/1024/1440 px; presentación a 375 px.
  Capturas de escritorio/móvil y evidencia en modo oscuro inspeccionadas.
- Nueve auditorías axe WCAG A/AA: cero infracciones automáticas. Queda una
  comprobación automática incompleta de contraste por solapamiento en el texto
  del diálogo. Se revisó visualmente y se midieron los colores efectivos: 6,11:1
  (`rgb(82,97,116)` sobre `rgb(250,251,253)`). Tab, Escape y retorno al botón
  que abrió el diálogo comprobados. Esto no equivale a certificación WCAG.
  Las etiquetas ARIA incorrectas encontradas
  durante la revisión fueron corregidas.
- Pruebas de broker: política, límites, self-approval, vencimiento exacto,
  resultado perdido tras aplicar, reinicio, revocación fallida/reintento,
  observación fallida, permisos ausentes sin regrant y limpieza de drift. Store
  cubre rollback de adopción y exclusión entre ejecutores además de los casos
  del primer hito. La CLI se prueba de init a revoke y cancelación del worker.
- El navegador necesitó un lanzador temporal con bibliotecas ya disponibles en
  `/tmp` y `--no-sandbox` por restricciones de namespaces. Se restringió a
  127.0.0.1. No se instalaron librerías en el host ni se versionó ese lanzador.

Estos resultados prueban el corte **local y de laboratorio**. No demuestran
identidad verificada, operación PostgreSQL con permisos restringidos,
vencimiento durante una caída, inventario total del clúster ni exposición
pública segura. Esos límites están en README, contrato del proveedor y pendientes.


## LEASE-007 — avance, sin cierre — 2026-10-22

Receta SQL explícita para un operador NOSUPERUSER/CREATEROLE/NOINHERIT, con
ADMIN-only sobre el rol base y pg_signal_backend. `lease check` verifica registro
y requisitos sin escribir eventos; init lo consulta antes de registrar contratos.
Grant vuelve a comprobar atributos y caminos de membresías antes de DDL.
La apertura para recuperación no bloquea la limpieza de permisos que se volvieron
inseguros. Configuración y alcance en [postgres-operator.md](postgres-operator.md).

Evidencia observada durante este avance:

- El recorrido positivo con operador separado pasó en PostgreSQL 18 descartable:
  aprobación, acceso, vencimiento, cierre de sesión SET ROLE, pérdida de permiso
  en conexión nueva y recuperación de una concesión con resultado perdido.
- Esa corrida completa todavía salió con error por el caso de opciones de escape:
  el fixture construía una URI con `+` para espacios. El parser de PostgreSQL/pgx
  conserva ese signo. Se corrigió a `%20` y se agregó regresión sin conexión.
- Pasan once casos de la consulta recursiva y sus reglas sobre un modelo de
  catálogo SQLite. No sustituyen la integración PostgreSQL real. La matriz
  ampliada de rutas peligrosas está escrita pero pendiente de ejecutar allí.
- `scripts/verify.sh` pasa con race; los casos de integración PostgreSQL se
  omiten cuando no se usa su script. No presentar ese gate como prueba del servidor.
- La repetición con clúster fue bloqueada por el sistema automático de revisión
  de permisos: cuota agotada, sin evaluación de seguridad de la acción. No se
  intentó eludirla ni se usaron bases o servicios existentes.
- El dueño decidió dejar pendiente esa repetición el 22/10 y pidió conservar
  el avance en un commit para retomarlo después.

Sin instalación productiva, publicación, alertas operativas ni cierre de
`LEASE-007`. Este avance continúa el corte local `d85637c`.

## LEASE-020 (parcial) — identidad verificada por Ozal — 2026-11-09

`lease serve` sirve consola y API para destinos reales sólo detrás de Ozal.
Nuevo paquete `internal/identity`: verificador de la aserción EdDSA de Ozal
(clave fijada, emisor, audiencia, ventana ≤60 s con tolerancia, `jti` sin
repetición con caché acotada que falla cerrada) y mapeo explícito de sujeto o
email a principal. `internal/web.NewOperational`: actor tomado de la identidad
(un `actor` en el cuerpo es 400), mutaciones con `Origin` público,
`X-Lease-Request` y JSON, vista filtrada por principal. La consola oculta el
selector de rol cuando el servidor informa una identidad verificada.

Evidencia observada:

- Tests Go con race: verificador (firma, emisor, audiencia, sujeto, `jti`,
  vida, vencido, futuro, firma alterada, repetición, configuraciones débiles,
  caché llena), middleware (loopback, IPv6, varias aserciones, sin mapeo) y
  modo operativo (actor del cuerpo, origen cruzado, falta de marcador,
  autoaprobación, aprobación, visibilidad del agente y del aprobador,
  repetición, recursos estáticos sin identidad).
- Sabotaje deliberado del filtro de visibilidad, del chequeo de origen y del
  rechazo de `actor`: cada uno hizo fallar su test; código restaurado.
- `scripts/test-ozal.mjs` contra Ozal `c9abeb1` real: PASS en los siete
  chequeos (tres passkeys, consola sin selector, atribución verificada,
  autoaprobación rechazada y aprobación activa, persona sin mapeo 403,
  acceso directo y cabeceras falsas 401, revocación).

Pendiente del mismo ID: aceptación publicada con passkeys físicas, destino
PostgreSQL real detrás de `serve` e identidad de agentes sin navegador.
