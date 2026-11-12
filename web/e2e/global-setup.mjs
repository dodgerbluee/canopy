/** E2E global setup: disposable schema + real API + real owner, all local.
 * Runs inside the Playwright container. The Go binaries are cross-compiled by
 * `make test-e2e` (host toolchain, GOOS=linux). Credentials here are the
 * disposable container defaults (same class as postgres/devpw), not secrets. */
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import pg from 'pg';

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, '../..');

const PG_ADMIN =
  process.env.E2E_PG_ADMIN ??
  'postgres://postgres:devpw@host.docker.internal:55432/canopy_t02?sslmode=disable';
const API_PORT = process.env.E2E_API_PORT ?? '8090';
const PASSPHRASE = 'e2e-dev-passphrase';

function run(bin, args, env) {
  return new Promise((res, rej) => {
    const p = spawn(bin, args, { env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
    let err = '';
    p.stderr.on('data', (d) => (err += d));
    p.on('exit', (code) => (code === 0 ? res() : rej(new Error(`${bin} exit ${code}: ${err}`))));
  });
}

export default async function globalSetup() {
  const schema = `t07e2e_${Date.now()}`;
  const admin = new pg.Client(PG_ADMIN);
  await admin.connect();
  await admin.query(`CREATE SCHEMA ${schema}`);
  await admin.query(`SET search_path TO ${schema}`);
  const migDir = join(repo, 'server/db/migrations');
  for (const name of readdirSync(migDir).filter((n) => n.endsWith('.sql')).sort()) {
    const mig = readFileSync(join(migDir, name), 'utf8');
    await admin.query(mig.split('-- +goose Down')[0]);
  }

  const dsn = `${PG_ADMIN}&search_path=${schema}`;
  const pwDir = mkdtempSync(join(tmpdir(), 'canopy-e2e-'));
  const pwFile = join(pwDir, 'passphrase');
  writeFileSync(pwFile, PASSPHRASE, { mode: 0o600 });

  await run(join(repo, '.artifacts/e2e/admin-linux'), ['owner', 'create', '--password-file', pwFile], {
    DATABASE_URL: dsn,
    CANOPY_ENV: 'test',
  });

  // Media bytes live in a disposable repo dir shared with the host worker
  // (scripts/e2e_with_worker.sh): the API runs in this container (/w), the
  // worker runs on the host, both must see the same ObjectStore root.
  const storageRoot = join(repo, '.artifacts/e2e/storage');
  mkdirSync(storageRoot, { recursive: true });
  writeFileSync(
    join(repo, '.artifacts/e2e/e2e-env.json'),
    JSON.stringify({ dsn: dsn.replace('host.docker.internal', 'localhost') }),
    { mode: 0o600 },
  );

  const api = spawn(join(repo, '.artifacts/e2e/api-linux'), [], {
    env: {
      ...process.env,
      DATABASE_URL: dsn,
      CANOPY_ENV: 'test',
      CANOPY_ADDR: `:${API_PORT}`,
      CANOPY_LOG_LEVEL: 'warn',
      CANOPY_STORAGE_ROOT: '/w/.artifacts/e2e/storage',
      // one API process serves every spec's sign-in; raise the per-key
      // budget above the suite's login count (prod default stays 10)
      CANOPY_LOGIN_LIMIT: '100',
      // T34: when the harness started a stub AI provider, the API must see
      // the same engine the worker does, or capabilities would lie about
      // availability. Unset elsewhere: text analysis stays honestly off.
      ...(process.env.E2E_AI_BASE_URL
        ? {
            CANOPY_AI_BASE_URL: process.env.E2E_AI_BASE_URL,
            CANOPY_AI_API_KEY: 'e2e-stub-key',
            CANOPY_AI_MODEL: 'e2e-stub',
            ...(process.env.E2E_AI_DAILY_RUN_LIMIT
              ? { CANOPY_AI_DAILY_RUN_LIMIT: process.env.E2E_AI_DAILY_RUN_LIMIT }
              : {}),
          }
        : {}),
    },
    stdio: ['ignore', 'inherit', 'inherit'],
  });

  const deadline = Date.now() + 15_000;
  let ready = false;
  while (!ready && Date.now() < deadline) {
    try {
      ready = (await fetch(`http://127.0.0.1:${API_PORT}/readyz`)).status === 200;
    } catch {
      // not up yet
    }
    if (!ready) await new Promise((r) => setTimeout(r, 250));
  }
  if (!ready) {
    api.kill('SIGTERM');
    throw new Error('e2e api did not become ready');
  }

  process.env.E2E_SCHEMA = schema;
  process.env.E2E_PG_DSN = dsn;
  process.env.E2E_PASSPHRASE = PASSPHRASE;

  return async () => {
    api.kill('SIGTERM');
    rmSync(storageRoot, { recursive: true, force: true });
    rmSync(join(repo, '.artifacts/e2e/e2e-env.json'), { force: true });
    try {
      await admin.query(`DROP SCHEMA IF EXISTS ${schema} CASCADE`);
    } catch {
      // disposable schema; next run uses a fresh one
    }
    await admin.end();
  };
}
