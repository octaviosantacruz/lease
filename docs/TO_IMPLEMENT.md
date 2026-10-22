# To implement — Lease

Fuente única de pendientes. Actualizado 22/10/2026. Los cierres viven en
[IMPLEMENTED.md](IMPLEMENTED.md). El dueño autorizó completar el producto y
la web, incluidas las piezas del núcleo antes reservadas como ejercicios.

El corte local `LEASE-001` a `LEASE-006` y `LEASE-040` está implementado. La
consola opera sólo laboratorio; PostgreSQL se opera por CLI local. No hay
publicación ni instalación productiva verificadas.

| ID | Trabajo | Estado y criterio de cierre |
|---|---|---|
| `LEASE-007` | Instalación real restringida de PostgreSQL | En curso: receta, preflight y recorrido positivo con operador separado; consulta de rutas cubierta en un modelo local. Falta repetir la matriz PostgreSQL ampliada tras corregir el fixture de URI (diferida por decisión del dueño el 22/10), alertas y recuperación en staging. Ver [evidencia y límites](postgres-operator.md). |
| `LEASE-008` | Credencial efímera PostgreSQL | Pendiente: distribución local segura y de un solo destinatario, revocación y sesiones probadas. El adaptador actual implementa elevación de un login existente. |
| `LEASE-010` | Proveedor de certificados SSH | Pendiente: contrato y pruebas de autenticación, sesiones existentes, revocación y alcance real del vencimiento. |
| `LEASE-011` | Break-glass y revisión posterior | Pendiente. Aprobación separada normal ya existe; no hay acceso de emergencia ni bypass del catálogo. |
| `LEASE-020` | Identidad verificada y API operativa | Pendiente: elegir OIDC/workload identity, mapear sujetos y permisos en servidor, pruebas de autorización y consola para destinos reales. La API actual sólo sirve al laboratorio loopback. |
| `LEASE-021` | Decider opcional | Pendiente: decisión tipada que sólo pueda restringir la política. No requerido por el corte actual. |
| `LEASE-030` | Integración Switchboard | Pendiente; requiere identidad verificada y contrato específico. Lease sigue independiente. |
| `LEASE-041` | Web ES/EN y publicación | Español local implementado. Falta traducción, revisión del dueño, licencia/distribución y hosting/corte explícito. La exportación estática no publica. |

No ampliar la exposición de la consola de laboratorio para convertirla en
operativa: el selector de actores no prueba identidad. Mantener próximos
proveedores independientes de Server Ops y de los datos de Custodia.
