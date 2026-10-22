# Web y consola de Lease

Implementadas el 22/10/2026 dentro del binario Go. Sin backend externo, Node en
runtime, CDN, fuentes remotas ni analítica. Rutas: `/` presentación,
`/guide/` instalación y límites, `/console/` laboratorio interactivo.

## Dirección visual

Lectura: presentación técnica para equipos de infraestructura y seguridad,
con lenguaje industrial sobrio. Aesthetic propia con CSS nativo; no se presenta
como un sistema oficial ni replica componentes de un proveedor. Parámetros:
variación 5, movimiento 2, densidad 4 en marketing y mayor en la consola.
Neutrales fríos, un acento azul, esquinas de 4 px, tipografía de sistema y números
monoespaciados. Un tema por página; la preferencia del sistema elige claro/oscuro.
No hay animación automática; sólo feedback de interacción.

La interfaz del producto es HTML semántico y JavaScript nativo sobre el núcleo
Go existente: agregar React a tres vistas y dos diálogos no aporta una capacidad
necesaria en este corte. Tablas en escritorio, filas como bloques en móvil,
filtros, foco visible, diálogos nativos, motivos escapados y mensajes de error,
carga y vacío. Los ejemplos representan al equipo ficticio de Litoral.

## Qué funciona

El selector simula Inés (solicitante), Mateo (aprobador) y un agente. Las acciones
hacen peticiones al backend y persisten solicitudes, decisiones y evidencia. No
son pasos de una animación ni estados sólo del navegador. El destino de laboratorio
conserva sus propios permisos en otro archivo para probar reinicios y diferencias.

La API admite únicamente el proveedor `lab`. Host exacto, origen propio, JSON
limitado y header de la consola son barreras del laboratorio, **no autenticación**.
El proceso sólo escucha loopback numérico. No exponer por un túnel ni eliminar
estas restricciones para apuntarlo a PostgreSQL. La consola real requiere
`LEASE-020`: identidad, autorización verificable y su revisión específica.

El worker ejecuta cada segundo. La vista distingue ventana vencida de un acceso
vigente en los contadores; el retiro depende del siguiente ciclo y del destino.
Cada evento de revocación final se registra después de observar ausencia.
La interfaz muestra fallos del worker y permisos esperados que no se encontraron.

## Assets y procedencia

- `internal/web/assets/media/access-key.webp`: imagen sintética original,
  generada con la herramienta integrada `image_gen`, después optimizada a WebP.
  No representa una integración ni hardware real.
- `internal/web/assets/media/console.webp`: captura original de la consola
  Go del laboratorio, 1360 × 900. Personas, permisos y empresa ficticios.
- HTML, CSS y JS propios; fuentes del sistema y símbolos tipográficos comunes.
  No hay fuentes, paquetes frontend ni imágenes de terceros en el runtime web.
- Dependencias del binario fijadas en `go.mod`/`go.sum`: pgx y SQLite, con sus
  dependencias transitivas. Esto no decide la licencia del producto ni autoriza
  una publicación; distribución pendiente de revisión del dueño.

Prompt final de la imagen (herramienta integrada, no CLI):

> Use case: product-mockup. Asset type: wide editorial image for Lease, a security access product website. Primary request: refined studio still life of a single brushed-aluminum physical access key, partly withdrawing from a precise cobalt-blue rectangular keyway block, a visual metaphor for temporary access ending. Scene: matte cool off-white studio ground, architectural daylight and clean soft shadows. Composition: horizontal 3:2, objects occupy right two thirds, generous clean negative space left, tactile realistic machining, understated technical industrial design. Palette: cool off-white #f4f6f8, graphite, muted cobalt #2857bd only. No screens, no UI, no text, no numbers, no padlock, no shield, no glowing effects, no watermark. High quality realistic product photography, not cyberpunk.

## Verificar y exportar

```sh
scripts/verify.sh
scripts/test-web.sh
./lease site export --out ./lease-site
```

La prueba de navegador necesita `agent-browser` y Chromium funcionales. Opcionales:
`LEASE_BROWSER_EXECUTABLE`, `LEASE_BROWSER_ARGS` y `LEASE_WEB_EVIDENCE` (directorio
para capturas e informes). No incluye rutas locales de bibliotecas del agente.
Requiere Python 3 para leer informes y `rg`, además de Go.

La exportación rechaza un destino no vacío. La web estática presenta el producto
y su guía. Sin backend, la consola explica cómo iniciar `lease demo`; no simula
un éxito. El host estático debe resolver `/guide/` y `/console/` a sus index.html
y aplicar sus propios headers. No se publica ni se integra con Portfolio al
exportar. Español disponible; inglés y publicación pendientes.
