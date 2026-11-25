// Acceptance of "lease serve" behind a real Ozal, with disposable state only.
//
//   OZAL_BIN=/abs/ozal LEASE_BIN=/abs/lease LEASE_PLAYWRIGHT=/abs/playwright/index.mjs \
//     node scripts/test-ozal.mjs
//
// Ozal signs the assertions; this harness never does. Three people enrol
// virtual passkeys: Inés (requester), Mateo (approver) and an outsider that
// Ozal admits but Lease does not map. Targets are the simulated lab provider.
import assert from 'node:assert/strict';
import {spawn, execFileSync} from 'node:child_process';
import {createServer} from 'node:net';
import {mkdtemp, writeFile, rm, copyFile} from 'node:fs/promises';
import {join, resolve, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const {OZAL_BIN: ozal, LEASE_BIN: lease, LEASE_PLAYWRIGHT: pw} = process.env;
if (!ozal || !lease || !pw) throw new Error('Set OZAL_BIN, LEASE_BIN and LEASE_PLAYWRIGHT (path to playwright/index.mjs)');
const {chromium} = await import(pw);

const root = await mkdtemp('/tmp/lease-ozal-run-');
const children = [];
const checks = [];
const errors = [];
let browser, stage = 'setup';
const token = randomBytes(24).toString('hex');
const delay = ms => new Promise(r => setTimeout(r, ms));
const freePort = () => new Promise((res, rej) => {
  const s = createServer(); s.once('error', rej);
  s.listen(0, '127.0.0.1', () => { const {port} = s.address(); s.close(() => res(port)); });
});
const start = (bin, args) => {
  const c = spawn(bin, args, {env: {...process.env, OZAL_ACCEPTANCE_ADMIN: token}, stdio: ['ignore', 'pipe', 'pipe']});
  c.stdout.resume(); c.stderr.resume(); children.push(c); return c; // output stays private
};
const stop = async c => {
  if (!c || c.exitCode !== null) return;
  const done = new Promise(r => c.once('exit', r)); c.kill('SIGTERM'); await done;
};
const ready = async (url, child, ok = s => s < 500) => {
  for (let i = 0; i < 150; i++) {
    if (child.exitCode !== null) throw new Error('service exited during startup');
    try { if (ok((await fetch(url)).status)) return; } catch {}
    await delay(100);
  }
  throw new Error('startup timed out: ' + url);
};
const json = (name, data) => writeFile(join(root, name), JSON.stringify(data), {mode: 0o600});

try {
  const [port, adminPort, leasePort] = await Promise.all([freePort(), freePort(), freePort()]);
  const login = `http://auth.acceptance.localhost:${port}`;
  const app = `http://lease.acceptance.localhost:${port}`;
  const direct = `http://127.0.0.1:${leasePort}`;

  await json('edge.json', {
    listen: `127.0.0.1:${port}`,
    admin: {listen: `127.0.0.1:${adminPort}`, token: 'env:OZAL_ACCEPTANCE_ADMIN'},
    auth: {cookie_domain: 'acceptance.localhost', passkeys: {rp_id: 'acceptance.localhost', login_url: login, display_name: 'Acceptance'}},
    identity_assertion: {key_dir: join(root, 'assertion')},
    routes: [{host: `lease.acceptance.localhost:${port}`, target: direct,
      access: {allowed_domains: ['acme.example']}, identity_assertion: {audience: 'lease'}}],
  });
  const edgeArgs = ['-config', join(root, 'edge.json'), '-db', join(root, 'state.db')];
  execFileSync(ozal, ['migrate', '-db', join(root, 'state.db')], {stdio: 'pipe'});
  const pub = execFileSync(ozal, ['assertion', 'public-key', '-config', join(root, 'edge.json')]);
  await writeFile(join(root, 'ozal.pub'), pub);

  await copyFile(join(repo, 'examples/lab.json'), join(root, 'policy.json'));
  await json('identity.json', {
    issuer: login, audience: 'lease', public_keys: [join(root, 'ozal.pub')], origin: app,
    emails: {'ines@acme.example': 'user:ines', 'mateo@acme.example': 'user:mateo'},
  });
  const leaseArgs = ['--db', join(root, 'broker.sqlite'), '--config', join(root, 'policy.json')];
  execFileSync(lease, ['init', ...leaseArgs], {stdio: 'pipe'});
  const backend = start(lease, ['serve', ...leaseArgs, '--identity', join(root, 'identity.json'), '--listen', `127.0.0.1:${leasePort}`]);
  await ready(`${direct}/api/state`, backend, s => s === 401); // up, and refusing anonymous calls
  const edge = start(ozal, edgeArgs);
  await ready(`http://127.0.0.1:${adminPort}/_/healthz`, edge, s => s === 200);

  const admin = async (method, path, body) => {
    const r = await fetch(`http://127.0.0.1:${adminPort}/_/api${path}`, {method,
      headers: {Authorization: `Bearer ${token}`, 'Content-Type': 'application/json'}, body: body ? JSON.stringify(body) : undefined});
    assert(r.ok, `admin ${path}: ${r.status}`); return r.status === 204 ? null : r.json();
  };
  browser = await chromium.launch({args: ['--no-sandbox']});
  const device = async email => {
    const context = await browser.newContext(); const page = await context.newPage();
    page.on('pageerror', e => errors.push(e.message));
    const cdp = await context.newCDPSession(page); await cdp.send('WebAuthn.enable');
    await cdp.send('WebAuthn.addVirtualAuthenticator', {options: {protocol: 'ctap2', transport: 'internal',
      hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true}});
    const inv = await admin('POST', '/people/invite', {email, name: email.split('@')[0], groups: [], ttl_minutes: 15});
    await page.goto(inv.link); await page.locator('#action').click();
    await page.locator('#who:not([hidden])').waitFor({timeout: 20000});
    return {context, page};
  };
  // call runs fetch inside the page at Lease's origin, so the browser sets
  // Origin and Ozal's cookie exactly as the console would.
  const call = (page, method, path, body) => page.evaluate(async ([method, path, body]) => {
    const r = await fetch(path, {method, headers: method === 'POST' ? {'Content-Type': 'application/json', 'X-Lease-Request': '1'} : {},
      body: body ? JSON.stringify(body) : undefined});
    let data = null; try { data = await r.json(); } catch {}
    return {status: r.status, data};
  }, [method, path, body]);

  stage = 'three passkey enrolments through Ozal';
  const ines = await device('ines@acme.example');
  const mateo = await device('mateo@acme.example');
  const outsider = await device('outsider@acme.example');
  checks.push(stage);

  stage = 'console shows the verified identity, no role selector';
  await ines.page.goto(`${app}/console/`);
  await ines.page.getByText('verificado por Ozal').waitFor({timeout: 10000});
  assert.equal(await ines.page.locator('#actor').count(), 0);
  checks.push(stage);

  stage = 'request is attributed to the verified person, not the browser';
  await ines.page.goto(`${app}/console/`);
  const forged = await call(ines.page, 'POST', '/api/request', {actor: 'user:mateo', entitlement: 'analytics-read', reason: 'x', seconds: 120});
  assert.equal(forged.status, 400);
  const req = await call(ines.page, 'POST', '/api/request', {entitlement: 'analytics-read', reason: 'INC-284', seconds: 120});
  assert.equal(req.status, 200, JSON.stringify(req.data));
  assert.equal(req.data.key.Principal, 'user:ines');
  checks.push(stage);

  stage = 'self-approval refused; approver grants';
  assert.equal((await call(ines.page, 'POST', '/api/approve', {id: req.data.id})).status, 403);
  await mateo.page.goto(`${app}/console/`);
  const ok = await call(mateo.page, 'POST', '/api/approve', {id: req.data.id});
  assert.equal(ok.status, 200, JSON.stringify(ok.data));
  let state;
  for (let i = 0; i < 50; i++) {
    state = await call(ines.page, 'GET', '/api/state');
    if (state.data.snapshot.grants.find(g => g.id === req.data.id)?.state === 'active') break;
    await delay(200);
  }
  assert.equal(state.data.snapshot.grants.find(g => g.id === req.data.id).state, 'active');
  assert.equal(state.data.identity.principal, 'user:ines');
  checks.push(stage);

  stage = 'person admitted by Ozal but unmapped in Lease is refused';
  await outsider.page.goto(`${app}/console/`).catch(() => {});
  assert.equal((await call(outsider.page, 'GET', '/api/state')).status, 403);
  checks.push(stage);

  stage = 'direct and forged-header access to Lease refused';
  assert.equal((await fetch(`${direct}/api/state`)).status, 401);
  assert.equal((await fetch(`${direct}/api/state`, {headers: {'X-Ozal-Assertion': 'eyJhbGciOiJFZERTQSJ9.e30.AAAA'}})).status, 401);
  assert.equal((await fetch(`${direct}/api/state`, {headers: {'X-Auth-Request-Email': 'mateo@acme.example'}})).status, 401);
  checks.push(stage);

  stage = 'revocation by the requester';
  const rev = await call(ines.page, 'POST', '/api/revoke', {id: req.data.id});
  assert.equal(rev.status, 200, JSON.stringify(rev.data));
  checks.push(stage);

  assert.deepEqual(errors, [], 'page errors: ' + errors.join('; '));
  console.log(JSON.stringify({result: 'PASS', checks, limits: [
    'isolated loopback HTTP under .localhost; browser virtual authenticators',
    'simulated lab target; public ingress and physical passkeys not tested']}, null, 2));
} catch (err) {
  console.error(JSON.stringify({result: 'FAIL', stage, checks, error: String(err?.message ?? err)}, null, 2));
  process.exitCode = 1;
} finally {
  await browser?.close().catch(() => {});
  for (const c of children) await stop(c);
  await rm(root, {recursive: true, force: true});
}
