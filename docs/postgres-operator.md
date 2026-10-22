# Operador PostgreSQL sin superusuario

Avance de `LEASE-007`, 22/10/2026. La receta y el recorrido del broker con un
operador separado se probaron en un clúster descartable. La ampliación final de
controles de membresías sigue pendiente de repetir contra PostgreSQL: el último
intento completo encontró un error de codificación de URI en el fixture, ya
corregido y cubierto sin sockets. No es un cierre de staging ni una instalación
productiva validada.

## Roles y responsabilidades

El administrador del clúster prepara la base, el rol de entitlement y sus permisos
sobre objetos concretos. Después ejecuta explícitamente
[postgres-operator.sql](../examples/postgres-operator.sql) con `psql`, sustituyendo
los identificadores por variables de identificador citadas:

```sh
psql -X -v ON_ERROR_STOP=1 \
  -v lease_operator=lease_operator \
  -v lease_base_role=lease_demo_reader \
  -v lease_database=lease_lab \
  -f examples/postgres-operator.sql
```

Ese comando usa la configuración local de conexión del administrador. No poner
contraseñas en argumentos ni en el SQL. La receta no configura autenticación:
el login nuevo no tiene contraseña asignada. Elegir localmente el mecanismo de
autenticación y los permisos de conexión adecuados a la instalación.

La receta crea un operador nuevo con LOGIN, CREATEROLE y NOINHERIT; sin
SUPERUSER, CREATEDB, REPLICATION ni BYPASSRLS. Le concede:

- ADMIN sobre el rol base, con INHERIT y SET desactivados.
- `pg_signal_backend`, heredable y sin ADMIN ni SET, para terminar sesiones.
- CONNECT a la base indicada.

No modifica el login beneficiario ni le da permisos permanentes. Para un catálogo
con varios roles base, un administrador debe conceder la misma membresía
ADMIN-only sobre cada uno. Un operador ya existente requiere revisar y aplicar
sus cambios manualmente; el script de creación no lo altera silenciosamente.

**El operador es de confianza.** CREATEROLE permite crear roles en todo el clúster;
ADMIN permite volver a concederse una membresía con INHERIT/SET, y
`pg_signal_backend` permite señalar otras sesiones no superusuario. Esta receta
reduce atributos respecto a un superusuario, pero no impone un sandbox de
namespace ni garantiza que un operador malicioso no lea los datos. La prueba
comprueba que no los hereda accidentalmente al ejecutar el adaptador.

Referencias: [atributos y concesión automática al creador](https://www.postgresql.org/docs/18/role-attributes.html),
[opciones de membresía](https://www.postgresql.org/docs/18/sql-grant.html) y
[terminación de sesiones](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-SIGNAL).

## Comprobar antes de conceder

```sh
./lease init --db ./pg-broker.sqlite --config examples/postgres.json
./lease check --db ./pg-broker.sqlite --config examples/postgres.json
```

El DSN se obtiene de la variable local nombrada en la configuración, como se
explica en [el contrato del proveedor](postgres.md). `init` hace preflight antes
de registrar entitlements; la migración del archivo sigue siendo una acción
explícita de `init`. `check` comprueba el registro local y los proveedores sin
conceder, revocar, matar sesiones ni escribir eventos. Sale con error si falta
un proveedor o un requisito. Su éxito no verifica identidad ni producción.

El proveedor comprueba que el operador conectado sea el usuario de sesión,
NOSUPERUSER/CREATEROLE/NOINHERIT, sin los otros atributos administrativos,
con uso de `pg_signal_backend`, EXECUTE sobre la función de terminación y
membresía directa ADMIN-only sobre cada rol base.

Sobre cada login beneficiario y rol base recorre las membresías del catálogo,
incluso las que no heredan ni permiten SET. Rechaza:

- Atributos SUPERUSER, CREATEROLE, CREATEDB, REPLICATION o BYPASSRLS en el camino.
- Roles predefinidos `pg_*` y acceso a otro login por membresía.
- ADMIN OPTION en cualquier nodo alcanzable: permitiría volver a conceder
  permisos fuera del vencimiento de Lease.
- Roles ausentes o atributo LOGIN distinto del contrato.

Es una política conservadora: puede rechazar una membresía actualmente inactiva.
No sustituye una revisión de ownership, ACL sobre objetos, funciones SECURITY
DEFINER, extensiones, PUBLIC, pg_hba.conf o cambios hechos por el administrador.
No deduce que un rol llamado `readonly` sea efectivamente sólo de lectura.

Grant repite los controles pertinentes dentro de su transacción antes del DDL.
También fija `createrole_self_grant` vacío para evitar membresías adicionales por
una preferencia de sesión y usa opciones ADMIN/INHERIT/SET explícitas.

## Recuperación cuando cambia un permiso

Abrir el proveedor no ejecuta el preflight de nuevas concesiones. Esto permite
retirar un grant anterior aunque sus sujetos o roles hayan dejado de cumplir la
política. `Check` y `Grant` sí lo rechazan. Observe marca los objetos afectados
como Broken; el broker los lleva a limpieza sin concederlos otra vez.

Revocar conserva las comprobaciones de ownership, retira las membresías del
objeto propio, termina sesiones y confirma ausencia. No aplica cambios a las
membresías peligrosas ajenas a Lease. Si el destino ya no permite terminar una
sesión o eliminar el objeto, deja `revoking` y necesita atención del operador.

## Evidencia y pendientes

- Antes de la última ampliación, pasó el recorrido con operador no superusuario:
  solicitud, aprobación, SELECT permitido al beneficiario, vencimiento, cierre
  de una sesión con SET ROLE y conexión nueva sin acceso. El operador no heredó
  SELECT, no pudo cambiar un rol ajeno ni crear un superusuario.
- Pasó recuperación real después de guardar Granting y aplicar el rol sin
  confirmar Active: al reabrir el broker retiró el permiso y verificó su auditoría.
- Las pruebas locales ejecutan once situaciones de la consulta recursiva sobre
  un modelo de catálogo SQLite y una regresión de codificación de URI usando el
  parser real de pgx. Esto no demuestra compatibilidad ni privilegios de PostgreSQL.
- La matriz PostgreSQL ampliada está escrita: atributos directos, rutas
  transitivas, rol predefinido, ADMIN delegable, login NOINHERIT, requisitos
  ausentes y limpieza tras invalidación. Su ejecución final sigue pendiente.
- `scripts/test-postgres.sh` exige initdb, pg_ctl y psql, crea sólo su clúster
  privado sin TCP, imprime el log del servidor si falla y limpia al terminar.
- Faltan alertas, recuperación operativa en staging y una aceptación explícita
  de la instalación. El vencimiento sigue dependiendo de un worker disponible.
