import { TestBed } from '@angular/core/testing';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionDocument, UpdateWorkspaceDocument } from '@canopy/contracts';
import { SessionService } from '../auth/session';
import { GraphQLClient, type GqlResult } from './client';

/**
 * T08 contract-harness specs: the client is driven by the frozen fixtures in
 * contracts/fixtures/api so envelope handling cannot drift from the contract.
 */
const fixtures = JSON.parse(
  readFileSync(resolve('..', 'contracts/fixtures/api/errors.json'), 'utf8'),
) as {
  cases: Array<{
    case: string;
    data?: Record<string, unknown> | null;
    errors?: Array<{ message: string; extensions?: { code?: string } }>;
  }>;
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('GraphQLClient', () => {
  let session: SessionService;
  let client: GraphQLClient;

  beforeEach(() => {
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: '3f2a1b0c-0000-4000-8000-000000000001',
      expiresAt: '2026-11-02T00:00:00Z',
      csrfToken: 'csrf-1',
    });
    client = TestBed.inject(GraphQLClient);
  });

  afterEach(() => vi.unstubAllGlobals());

  function lastRequest(): { url: string; init: RequestInit & { headers: Record<string, string> } } {
    const call = vi.mocked(fetch).mock.calls.at(-1) as [string, RequestInit & { headers: Record<string, string> }];
    return { url: call[0], init: call[1] };
  }

  it('POSTs the printed generated document with CSRF and same-origin credentials', async () => {
    const data = {
      session: {
        authenticated: true,
        workspace: {
          id: 'w1',
          displayName: 'My garden',
          timezone: 'UTC',
          defaultCurrency: 'USD',
          settingsRevision: 1,
          location: null,
        },
      },
    };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ data })));

    const res = await client.query(SessionDocument);

    expect(res).toEqual<GqlResult<unknown>>({ kind: 'ok', data, errors: [] });
    const req = lastRequest();
    expect(req.url).toBe('/api/graphql');
    expect(req.init.method).toBe('POST');
    expect(req.init.credentials).toBe('same-origin');
    expect(req.init.headers['Content-Type']).toBe('application/json');
    expect(req.init.headers['X-CSRF-Token']).toBe('csrf-1');
    const body = JSON.parse(req.init.body as string);
    expect(body.query).toContain('query Session');
    expect(body.variables).toBeUndefined();
  });

  it('sends variables for mutations', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ data: { updateWorkspace: { workspace: null, problems: [], clientMutationId: 'cm1' } } })),
    );
    const variables = {
      input: { displayName: 'Back yard', expectedRevision: 1 },
      clientMutationId: 'cm1',
    };
    await client.mutate(UpdateWorkspaceDocument, variables);
    const body = JSON.parse(lastRequest().init.body as string);
    expect(body.query).toContain('mutation UpdateWorkspace');
    expect(body.variables).toEqual(variables);
  });

  it('omits the CSRF header when there is no session', async () => {
    session.info.set(null);
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ data: null }, 401)));
    await client.query(SessionDocument);
    expect(lastRequest().init.headers['X-CSRF-Token']).toBeUndefined();
  });

  it('401 marks the session expired and reports kind auth, not payload errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ type: 'https://canopy.app/problems/unauthenticated', title: 'Unauthenticated', status: 401 }, 401),
      ),
    );
    const res = await client.query(SessionDocument);
    expect(res).toEqual({ kind: 'auth' });
    expect(session.state()).toBe('anon');
    expect(session.ended()).toBe('expired');
  });

  it('403 (CSRF mismatch) is auth but keeps a live session', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ type: 'https://canopy.app/problems/csrf', title: 'CSRF', status: 403 }, 403)),
    );
    const res = await client.query(SessionDocument);
    expect(res).toEqual({ kind: 'auth' });
    expect(session.state()).toBe('authed');
  });

  it('passes every contract error fixture through as ok-with-envelope', async () => {
    for (const c of fixtures.cases) {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ data: c.data ?? null, errors: c.errors ?? [] })));
      const res = await client.mutate(UpdateWorkspaceDocument, { input: { displayName: 'x', expectedRevision: 1 } });
      expect(res.kind, c.case).toBe('ok');
      if (res.kind !== 'ok') continue;
      expect(res.data, c.case).toEqual(c.data ?? null);
      expect(res.errors, c.case).toEqual(c.errors ?? []);
    }
    // The stale-revision case keeps its typed problem inside data — proof
    // payload problems never collapse into transport kinds.
    const conflict = fixtures.cases.find((c) => c.case === 'REVISION_CONFLICT');
    expect(conflict).toBeDefined();
  });

  it('reports cancellation as kind aborted', async () => {
    const err = Object.assign(new Error('The operation was aborted.'), { name: 'AbortError' });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(err));
    const controller = new AbortController();
    expect(await client.query(SessionDocument, undefined, controller.signal)).toEqual({ kind: 'aborted' });
  });

  it('reports transport failure as kind network', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('fetch failed')));
    expect(await client.query(SessionDocument)).toEqual({ kind: 'network' });
  });

  it('a non-JSON body is a network failure, not a GraphQL error list', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>gateway</html>', { status: 502 })));
    expect(await client.query(SessionDocument)).toEqual({ kind: 'network' });
  });
});
