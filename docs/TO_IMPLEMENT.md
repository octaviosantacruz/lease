# To implement — Lease

Fuente única de pendientes. Actualizado 22/10/2026. Los cierres viven en
[IMPLEMENTED.md](IMPLEMENTED.md). El dueño autorizó completar el producto y
la web, incluidas las piezas del núcleo antes reservadas como ejercicios.

El corte local `LEASE-001` a `LEASE-006` y `LEASE-040` está implementado. La
consola opera sólo laboratorio; PostgreSQL se opera por CLI local. No hay
publicación ni instalación productiva verificadas.

| ID | Trabajo | Estado y criterio de cierre |
|---|---|---|
| `LEASE-007` | Instalación real restringida de PostgreSQL | En curso: receta, preflight y recorrido positivo con operador separado; consulta de rutas cubierta en un modelo local. La matriz PostgreSQL ampliada pasó el 09/11 en un clúster descartable (`scripts/test-postgres.sh`, incluido `TestRestrictedOperatorBrokerLifecycle`) y corre en CI. Faltan alertas y recuperación en staging. Ver [evidencia y límites](postgres-operator.md). |
| `LEASE-008` | Credencial efímera PostgreSQL | Pendiente: distribución local segura y de un solo destinatario, revocación y sesiones probadas. El adaptador actual implementa elevación de un login existente. |
| `LEASE-010` | Proveedor de certificados SSH | Pendiente: contrato y pruebas de autenticación, sesiones existentes, revocación y alcance real del vencimiento. |
| `LEASE-011` | Break-glass y revisión posterior | Pendiente. Aprobación separada normal ya existe; no hay acceso de emergencia ni bypass del catálogo. |
| `LEASE-020` | Identidad verificada y API operativa | En curso, 09/11: `lease serve` detrás de Ozal (aserción firmada, mapeo de sujetos, CSRF, vista mínima) con aceptación automática contra Ozal real ([docs/ozal.md](ozal.md)). Falta: aceptación publicada con passkeys físicas y un destino PostgreSQL real detrás de `serve`; identidad de agentes (workload identity) sin navegador. |
| `LEASE-021` | Decider opcional | Pendiente: decisión tipada que sólo pueda restringir la política. No requerido por el corte actual. |
| `LEASE-030` | Integración Switchboard | Pendiente; requiere identidad verificada y contrato específico. Lease sigue independiente. |
| `LEASE-041` | Web ES/EN y publicación | Español local implementado. Falta traducción, revisión del dueño, licencia/distribución y hosting/corte explícito. La exportación estática no publica. |

No ampliar la exposición de la consola de laboratorio para convertirla en
operativa: el selector de actores no prueba identidad. Mantener próximos
proveedores independientes de Server Ops y de los datos de Custodia.

## Dirección — 2026-11-09

**Decisión del dueño: se mantiene como producto (servicio), no como paquete, y se
reposiciona junto a Ozal.** Ozal decide quién sos y si entrás; Lease decide qué
privilegio temporal recibís sobre bases y destinos; AuditAnchor puede anclar su
auditoría encadenada.

- **Servicio, no librería.** Lease tiene estado propio (libro de grants), un
  worker que debe correr para revocar a tiempo, aprobaciones humanas y
  credenciales privilegiadas de los destinos. Embeberlo en el edge expuesto
  (Ozal) pondría esas credenciales en el componente con más superficie: el
  edge no debe poder elevar privilegios en bases. Proceso separado, mínimo
  privilegio, identidad delegada a Ozal.
- **`LEASE-020` se resuelve con Ozal.** Usar la aserción firmada que Ozal ya
  emite (la misma que Custodia está aprendiendo a verificar) en lugar de un
  OIDC propio: reutiliza ese trabajo y cierra el hueco de `--as`.
- **`LEASE-030`** (integración Switchboard) se reevalúa como integración con
  el edge actual (Ozal), con el mismo requisito de identidad verificada.
- **Portafolio:** entra después de `LEASE-020` con identidad de Ozal y una demo
  pública. Hoy el selector de actores simula identidad y el repo es privado; un
  lector de seguridad lo notaría. Encuadre propuesto: "Acceso temporal para
  personas y agentes", PAM/JIT.
- **Prioridad:** después de cerrar la verificación de identidad Ozal↔Custodia;
  no abrir proveedores nuevos (`LEASE-008`/`010`) antes de `LEASE-020`.
- `AGENTS.md` cita `../AGENTS.md` y `../TO_IMPLEMENT.md` (`IDEA-009`) del
  workspace: esos archivos ya no existen.
