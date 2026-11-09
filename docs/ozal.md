# Lease detrás de Ozal (`LEASE-020`)

`lease serve` publica la consola y la API para destinos reales **sólo detrás de
Ozal**. Ozal autentica a la persona (passkeys) y firma por cada petición una
aserción corta (EdDSA, ≤60 s, `jti` único) para la audiencia de Lease. Lease
fija la clave pública de Ozal, verifica la aserción, rechaza repeticiones y
mapea el sujeto verificado a un principal de la política. El navegador no
elige el actor: un campo `actor` en el cuerpo es un error.

## Qué garantiza

| Control | Dónde |
|---|---|
| Sólo Ozal llega a Lease | `serve` escucha en loopback numérico y rechaza pares no loopback (401 `not_through_ozal`) |
| Identidad firmada, no cabeceras | `X-Ozal-Assertion` verificada con la clave fijada; cabeceras `X-Auth-Request-*` se ignoran |
| Sin repetición | caché de `jti` hasta el vencimiento; si se llena, rechaza (falla cerrado) |
| Sólo personas mapeadas | sujeto u email sin mapeo: 403 `unmapped_subject`, aunque Ozal lo haya admitido |
| Política sin cambios | solicitar, aprobar (nunca la propia), denegar y revocar siguen la política JSON |
| CSRF | mutaciones exigen `Origin` igual al origen público, `X-Lease-Request: 1` y JSON |
| Vista mínima | cada persona ve los permisos que puede pedir o aprobar, sus solicitudes y las que puede aprobar, y sólo los eventos de esas |

No cubre: alta disponibilidad, varias instancias sobre el mismo libro, ni
revocación en plazo si el broker cae (`NativeExpiry=false` en PostgreSQL).

## Configuración

Ruta en Ozal (fragmento):

```json
{
  "host": "lease.example.com",
  "target": "http://127.0.0.1:8790",
  "access": { "allowed_groups": ["lease-users"] },
  "identity_assertion": { "audience": "lease" }
}
```

Clave pública: `ozal assertion public-key -config edge.json > ozal.pub`.

`identity.json` de Lease:

```json
{
  "issuer": "https://auth.example.com",
  "audience": "lease",
  "public_keys": ["/etc/lease/ozal.pub"],
  "origin": "https://lease.example.com",
  "subjects": { "SUJETO-ESTABLE-DE-OZAL": "user:mateo" },
  "emails": { "ines@example.com": "user:ines" }
}
```

- `issuer` es la URL de login de Ozal; `audience` la de la ruta.
- `origin` debe ser `https`; `http` sólo se acepta bajo `*.localhost` (pruebas).
- `subjects` gana sobre `emails`. Preferir sujetos: son estables; el email
  sirve cuando el sujeto aún no se conoce. Los principales deben ser los que
  nombra la política (`requesters`/`approvers`).
- `skew` opcional (por defecto `5s`, máximo `30s`); `max_ids` opcional.

```sh
lease init  --db broker.sqlite --config policy.json
lease serve --db broker.sqlite --config policy.json --identity identity.json --listen 127.0.0.1:8790
```

`serve` mantiene el lock del libro y reconcilia cada segundo: no ejecutar
`lease run` ni comandos que operen sobre el mismo libro mientras corre.

## Aceptación con Ozal real

```sh
OZAL_BIN=/abs/ozal LEASE_BIN=/abs/lease \
LEASE_PLAYWRIGHT=/abs/node_modules/playwright/index.mjs \
  node scripts/test-ozal.mjs
```

Usa los binarios reales, estado descartable en `/tmp`, el proveedor simulado
de laboratorio y passkeys virtuales de Chromium para tres personas: Inés
(solicitante), Mateo (aprobador) y una persona que Ozal admite pero Lease no
mapea. Verifica consola sin selector, atribución verificada (un `actor` en el
cuerpo se rechaza), autoaprobación rechazada, aprobación y activación,
rechazo de la persona sin mapeo, rechazo de acceso directo y de cabeceras
falsas, y revocación. No acredita ingreso público, passkeys físicas ni un
destino PostgreSQL real.

El verificador sigue al de Custodia (`server/internal/identity`). Cuando Ozal
publique un paquete de verificación, ambos deberían importarlo en lugar de
mantener copias.
