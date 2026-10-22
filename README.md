# Lease

Acceso temporal para personas y agentes. Broker local en Go con catálogo,
aprobación separada, concesión, revocación activa, reconciliación y evidencia
persistente en SQLite.

**Primer corte local, 22/10/2026:** CLI operativa, proveedor PostgreSQL probado
en un clúster descartable, consola de laboratorio y web de presentación
exportable. No hay despliegue público ni identidad OIDC. La CLI confía en el
operador del sistema: `--as` atribuye una acción, no autentica a una persona.

## Probar sin destinos reales

```sh
go build -o lease ./cmd/lease
./lease demo --dir ./lease-lab --listen 127.0.0.1:8789 --seed
```

Abrir la URL impresa. `/` presenta el producto; `/console/` opera el laboratorio;
`/guide/` explica cómo usarlo. Inés solicita, Mateo aprueba y el agente de
diagnóstico usa la misma política. El selector de roles es una simulación.
Probá una ventana de diez segundos para ver la revocación automática.

La demo usa un destino simulado **persistente y separado del libro**. Al
reiniciar, recupera intentos y reconcilia. `--seed` sólo llena un libro vacío;
no reinicia ni borra datos. La API de la consola rechaza destinos reales y el
servidor exige loopback numérico, Host exacto y origen propio para mutaciones.

## CLI local

```sh
./lease init --db ./broker.sqlite --config examples/lab.json
./lease request --db ./broker.sqlite --config examples/lab.json \
  --as user:ines --entitlement analytics-read --ttl 5m --reason INC-284
./lease approve --db ./broker.sqlite --config examples/lab.json \
  --as user:mateo --id ID_DEVUELTO
./lease list --db ./broker.sqlite --config examples/lab.json
./lease revoke --db ./broker.sqlite --config examples/lab.json \
  --as user:ines --id ID_DEVUELTO
./lease run --db ./broker.sqlite --config examples/lab.json --interval 2s
```

También existen `deny`, `events` y `reconcile` (una pasada). `run` libera su
lock entre pasadas; los comandos pueden operar entre ellas. Una operación larga
puede hacer que otra CLI devuelva un error de lock; no hay ejecución concurrente
de efectos. El worker recarga conexiones y reintenta tras errores, pero conserva
la política cargada al iniciarlo: reiniciarlo tras cambiar el archivo.

La ventana comienza al solicitar y no se renueva al aprobar. Una autoaprobación
no existe en este corte. El solicitante no puede aprobar su propio grant ni
eludir el catálogo, TTL o justificación con la CLI/API del broker.

La CLI y sus archivos están bajo un mismo operador de confianza. La separación
de actores es una regla funcional; alguien con acceso al binario y los archivos
puede elegir otro `--as`. Una separación real entre personas necesita identidad
verificada, pendiente de `LEASE-020`.

## PostgreSQL

[Contrato, permisos y límites](docs/postgres.md). Usa roles temporales NOLOGIN
sobre logins existentes; no genera ni distribuye contraseñas. Revoca membresías,
termina **todas las sesiones del login afectado en el clúster** y elimina el rol
administrado. Sólo observa su namespace. El modo de credencial efímera sigue
pendiente; este primer adaptador implementa elevación.

La revocación en plazo depende del worker. `NativeExpiry=false`: una caída del
broker puede extender el acceso. La integración se verifica sin usar bases del
host:

```sh
scripts/test-postgres.sh
```

Requiere PostgreSQL 18+ instalado (`LEASE_PG_BIN` permite elegir su directorio).
El script crea un clúster privado en `/tmp`, sin listener TCP, y lo destruye al
terminar. No usar credenciales ni destinos de producción para esta prueba.

## Web estática

```sh
./lease site export --out ./lease-site
```

Exporta HTML/CSS/JS e imágenes a un directorio nuevo o vacío, sin publicar.
La presentación y guía funcionan sin backend; la consola muestra instrucciones
si no existe `/api/state`. El runtime no necesita Node, CDN ni recursos externos.
[Diseño y procedencia](docs/WEB.md).

## Desarrollo

```sh
scripts/verify.sh                   # gofmt, vet, race, build, diff
scripts/test-postgres.sh            # integración real descartable
scripts/test-web.sh                 # navegador, requiere agent-browser/Chromium
```

`go test ./...` deja la integración PostgreSQL skipped cuando no se ejecuta su
script; no representa esa prueba real. [Evidencia observada](docs/IMPLEMENTED.md).

```
cmd/lease/             CLI, worker y servidor de laboratorio
internal/grant/        máquina de estados pura
internal/reconcile/    comparación pura, con identidad por GrantID
internal/broker/       política, ejecutor y recuperación
internal/provider/     contrato, laboratorio y PostgreSQL
internal/store/        migración explícita, intentos y auditoría encadenada
internal/web/          presentación, guía, consola y API sólo de laboratorio
```

Los paquetes puros no leen reloj ni hacen I/O. La implementación completa del
núcleo fue autorizada por el dueño el 22/10; las pruebas originales se conservaron.
[Diseño](docs/architecture/design.md), [persistencia](docs/storage.md),
[pendientes](docs/TO_IMPLEMENT.md). Lease no administra servicios/puertos ni
sustituye un proveedor de identidad, una bóveda o el RBAC de aplicaciones.
