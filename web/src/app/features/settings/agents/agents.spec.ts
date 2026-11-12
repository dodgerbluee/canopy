import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GraphQLClient } from '../../../core/graphql/client';
import { AgentConnections } from './agents';
const grant = {
  id: '11111111-1111-4111-8111-111111111111',
  name: 'Hermes',
  scopes: ['garden:read'],
  plantIds: [],
  expiresAt: '2026-12-01T00:00:00Z',
  revokedAt: null,
  revision: 1,
  lastUsedAt: null,
  lastTool: null,
  createdAt: '2026-11-06T00:00:00Z',
};
const settings = {
  enabled: true,
  endpoint: 'http://localhost:8080/mcp',
  scopeOptions: ['garden:read', 'logs:write'],
  grants: [grant],
  actions: [],
};
const ok = (data: unknown) => ({ kind: 'ok', errors: [], data });
describe('Agent connections', () => {
  let query: ReturnType<typeof vi.fn>, mutate: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    query = vi.fn(async (doc: any) => {
      const name = doc.definitions.find((d: any) => d.kind === 'OperationDefinition')?.name.value;
      return ok(
        name === 'AgentSettings'
          ? { agentSettings: structuredClone(settings) }
          : { plants: { items: [], pageInfo: { hasNextPage: false, endCursor: null } } },
      );
    });
    mutate = vi.fn();
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: GraphQLClient, useValue: { query, mutate } }],
    });
  });
  afterEach(() => TestBed.resetTestingModule());
  async function render() {
    const f = TestBed.createComponent(AgentConnections);
    f.detectChanges();
    await vi.waitFor(() => expect(f.componentInstance.data()).not.toBeNull());
    return f;
  }
  it('shows safe setup snippets with a token placeholder', async () => {
    const f = await render(),
      c = f.componentInstance;
    expect(c.snippet()).toContain('${CANOPY_AGENT_TOKEN}');
    expect(c.token()).toBe('');
    expect(c.snippet()).not.toContain('canopy_agent.');
  });
  it('opens a clean form with read permissions and no validation errors', async () => {
    const f = await render(),
      c = f.componentInstance;
    await c.edit();
    expect(c.formError()).toBe('');
    expect(c.draft.scopes).toEqual(['garden:read', 'knowledge:read', 'care:read']);
    expect(c.draft.plantIds).toEqual([]);
  });
  it('reveals a newly issued token once and clears it on dismissal', async () => {
    const f = await render(),
      c = f.componentInstance;
    await c.edit();
    c.draft.name = 'New agent';
    mutate.mockResolvedValue(
      ok({
        saveAgentGrant: {
          grant: { ...grant, name: 'New agent' },
          token: 'fixture-one-time-token',
          problems: [],
        },
      }),
    );
    await c.save();
    expect(c.reveal()).toBe(true);
    expect(c.tokenVisible()).toBe(false);
    expect(c.token()).toBe('fixture-one-time-token');
    c.closeReveal();
    expect(c.token()).toBe('');
    expect(c.reveal()).toBe(false);
  });
  it('keeps the draft open when permissions conflict', async () => {
    const f = await render(),
      c = f.componentInstance;
    await c.edit(grant);
    mutate.mockResolvedValue(
      ok({
        saveAgentGrant: {
          grant: null,
          token: null,
          problems: [{ message: 'Connection changed. Reload before saving.' }],
        },
      }),
    );
    await c.save();
    expect(c.open()).toBe(true);
    expect(c.formError()).toContain('Reload');
    expect(c.draft.name).toBe('Hermes');
  });
  it('does not submit an empty restricted plant list', async () => {
    const f = await render(),
      c = f.componentInstance;
    await c.edit(grant);
    c.restricted = true;
    await c.save();
    expect(mutate).not.toHaveBeenCalled();
    expect(c.formError()).toContain('Choose at least one plant');
  });
});
