# PostgreSQL: elevación local y revocación

Contrato implementado de `LEASE-004`, 22/10/2026. Probado con PostgreSQL 18 en
clúster descartable. No es una validación de una instalación de producción.

## Alcance

Se mapean explícitamente principal lógico → login PostgreSQL y entitlement →
rol NOLOGIN ya existente. El operador configura previamente los permisos reales
del rol base (por ejemplo, SELECT sobre tablas concretas). Lease no crea las
bases, tablas, roles base o logins ni adopta credenciales de sistemas existentes.

Para cada GrantID crea un rol NOLOGIN de nombre determinista dentro de un
namespace dedicado. Le concede el rol base y lo asigna al login. Guarda en el
comentario del objeto su GrantID, key, vencimiento y mapeos SQL originales. La
creación, comentario y membresías se confirman en una transacción. Un mismo
GrantID con parámetros diferentes se rechaza.

Esto implementa **elevación**, no una credencial efímera. La contraseña, el método
de autenticación y los permisos ajenos a Lease del login siguen bajo el operador.
El adaptador no afirma que ese login parta sin otros privilegios.

## Configuración

Ver [ejemplo sin secretos](../examples/postgres.json). `dsn_env` nombra una
variable local; el JSON no contiene el DSN ni se imprime su valor. Para cargarlo
sin ponerlo en el historial:

```sh
read -r -s -p 'DSN del laboratorio: ' LEASE_PG_DSN
printf '\n'
export LEASE_PG_DSN
./lease init --db ./pg-broker.sqlite --config examples/postgres.json
```

Ingresar el valor sólo en la terminal local. No copiarlo a tickets, chats o
logs. Usar TLS verificado para un destino remoto y conexión directa, sin pooler
transaccional para las operaciones de administración. El driver tiene un pool
máximo de dos conexiones y un timeout de conexión de cinco segundos.

Los logins mapeados deben existir, permitir LOGIN y ser distintos del operador,
sin SUPERUSER, CREATEROLE, CREATEDB, REPLICATION ni BYPASSRLS. Los roles base
no pueden tener esos atributos ni LOGIN. Estos controles no prueban que sus
privilegios sobre objetos sean mínimos: el operador debe
revisarlos. La ampliación de `LEASE-007` recorre membresías y rechaza rutas
privilegiadas o delegables antes de Grant; ver su estado de verificación abajo. El adaptador nunca concede SQL formado con identificadores sin citar.

El operador necesita crear y eliminar los roles temporales, administrar las
membresías correspondientes y terminar las sesiones de los logins mapeados.
La integración usa un superusuario sólo para preparar el clúster descartable.
La [receta del operador](postgres-operator.md) opera el adaptador con un login
separado sin superusuario. El recorrido positivo pasó; la matriz final de
controles adicionales está pendiente de repetición. No es una instalación
productiva validada ni un sandbox que limite CREATEROLE al namespace.

No cambiar namespace/destino ni eliminar los contratos del catálogo mientras
haya grants recuperables u objetos administrados. Los entitlements son inmutables
por ID. Los mapeos SQL originales se conservan en el objeto para su limpieza;
renombrar/eliminar manualmente roles o comentarios requiere revisión del operador.

## Revocación

1. Lease confirma `revoking` en SQLite antes de llamar al adaptador.
2. El adaptador comprueba que el objeto pertenece al namespace y que su
   metadata coincide con su nombre y key. Retira las membresías y hace commit.
3. Termina las sesiones del login original en **todo el clúster**, incluidas las
   de otras bases y las que usaron `SET ROLE`. Esto afecta también sesiones que
   no usaron Lease. Usar logins dedicados al acceso temporal.
4. Elimina el rol temporal. No ejecuta `DROP OWNED`: si el rol posee objetos o
   tiene dependencias que impiden borrarlo, devuelve error y deja limpieza pendiente.
5. El broker consulta el destino y sólo registra `revoked` cuando el objeto ya
   no aparece. Si no se puede observar, conserva `revoking` y registra el fallo.

Si se perdió el resultado de Grant, se puede reconstruir el nombre desde
GrantID sin handle local. Revoke repetido admite ausencia; también termina las
sesiones del login configurado si el objeto ya desapareció, para no interpretar
la ausencia del rol como prueba de cierre de sesiones. Un objeto con ownership
no reconocible nunca se borra por el prefijo solamente.

## Observación y drift

Observe inventaría sólo roles del namespace dedicado. Comprueba metadata,
atributos y las membresías directas esperadas. La ampliación en curso también
rechaza rutas privilegiadas o delegables desde el login o el rol base. Un objeto propio con membresías
o atributos inesperados se marca `Broken`: el broker revoca el grant y lo
reporta como incompleto. Un objeto ausente se reporta; no se vuelve a conceder.
Los objetos huérfanos reconocibles se adoptan atómicamente como intentos de
limpieza `revoking`, sin convertirse en solicitudes aprobables.

Un rol con prefijo pero metadata inválida produce error para revisión manual.
La observación no inventaría todos los privilegios permanentes de un clúster,
todos los privilegios sobre objetos de los roles base. El control de
membresías no pretende inventariar ni eliminar todas las rutas ajenas a Lease. No es una medición
completa de zero standing privilege.

## Vencimiento y fallos

`NativeExpiry=false`. PostgreSQL no retira automáticamente estas membresías ni
termina sesiones en `ExpiresAt`. El worker actúa en el primer ciclo disponible;
si Lease o el destino están caídos, el acceso puede sobrevivir al plazo. Nunca
presentar esto como vencimiento garantizado durante una caída.

`VALID UNTIL` sólo vence contraseñas, no el rol ni autenticaciones que no usan
contraseña. Este adaptador NOLOGIN no depende de esa opción. Referencias:
[CREATE ROLE](https://www.postgresql.org/docs/18/sql-createrole.html),
[DROP ROLE](https://www.postgresql.org/docs/18/sql-droprole.html) y
[terminación de sesiones](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-SIGNAL).

## Prueba real descartable

`scripts/test-postgres.sh` crea su propio clúster PostgreSQL sin TCP, con socket
Unix privado y autenticación trust sólo dentro del laboratorio. Comprueba
acceso denegado antes, concesión/idempotencia, observación, conflicto de
parámetros, acceso tras vencimiento sin worker, detección de membresía extra,
revocación sin Ref, cierre de una sesión existente con `SET ROLE`, pérdida de
acceso en sesiones nuevas, revocación repetida y protección de un rol ajeno.

Los casos del broker cubren resultado desconocido, reintento de revocación y
limpieza tras reinicio con un destino simulado persistente. La prueba real no
simula corte eléctrico ni demuestra todos los fallos de un servidor remoto.


## Preflight y estado del operador

`lease check --db PATH --config FILE` verifica evidencia local y requisitos del
proveedor sin modificar privilegios ni eventos. `Grant` repite sus controles,
mientras `Revoke` conserva acceso a la limpieza de objetos propios cuando la
política de los sujetos cambió. Ver [receta, límites y evidencia](postgres-operator.md).
Esta ampliación no se presenta como integración final verde hasta repetir
`scripts/test-postgres.sh` con todos sus nuevos casos.
