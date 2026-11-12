import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GraphQLClient } from '../../core/graphql/client';
import { AiStatusPanel } from './ai-status';
import type {
  AIConnectionSettingsFieldsFragment,
  AIConnectionFieldsFragment,
} from '@canopy/contracts';
const connection: AIConnectionFieldsFragment = {
  id: '11111111-1111-4111-8111-111111111111',
  name: 'My local AI',
  protocol: 'OPENAI_COMPATIBLE',
  endpoint: 'http://127.0.0.1:1234/v1',
  model: 'my-model',
  deploymentLocal: true,
  supportsVision: true,
  externalConsent: false,
  hasAPIKey: true,
  reasoningEffort: 'none',
  tokenLimitField: 'MAX_TOKENS',
  revision: 2,
  lastTestedAt: null,
  lastTestSucceeded: null,
  lastTestMessage: null,
};
const settings: AIConnectionSettingsFieldsFragment = {
  connections: [connection],
  bindings: [
    { feature: 'GARDEN', mode: 'DEFAULT', connectionId: null, revision: 0 },
    { feature: 'VISION', mode: 'DEFAULT', connectionId: null, revision: 0 },
    { feature: 'RECEIPT', mode: 'DEFAULT', connectionId: null, revision: 0 },
  ],
  deploymentDefaults: [
    {
      feature: 'GARDEN',
      protocol: 'OPENAI_COMPATIBLE',
      endpoint: connection.endpoint,
      model: 'strata-model',
      deploymentLocal: true,
      supportsVision: true,
      hasAPIKey: true,
      reasoningEffort: 'none',
      tokenLimitField: 'MAX_TOKENS',
    },
  ],
};
const ok = (data: unknown) => ({ kind: 'ok', errors: [], data });
describe('AI connection settings', () => {
  let query: ReturnType<typeof vi.fn>, mutate: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    query = vi.fn().mockResolvedValue(ok({ aiConnections: structuredClone(settings) }));
    mutate = vi.fn();
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: GraphQLClient, useValue: { query, mutate } }],
    });
  });
  afterEach(() => TestBed.resetTestingModule());
  async function render() {
    const f = TestBed.createComponent(AiStatusPanel);
    f.detectChanges();
    await vi.waitFor(() => expect(f.componentInstance.data()).not.toBeNull());
    f.detectChanges();
    return f;
  }
  it('opens a clean modal and retains a saved key by omission', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.edit(connection);
    f.detectChanges();
    expect(c.apiKey).toBe('');
    expect(c.formError()).toBe('');
    expect(document.querySelector('[role=dialog]')).not.toBeNull();
    mutate.mockResolvedValue(ok({ saveAIConnection: { connection, problems: [] } }));
    await c.save();
    const vars = mutate.mock.calls[0][1];
    expect(vars.input.apiKey).toBeUndefined();
    expect(vars.input.clearAPIKey).toBe(false);
    expect(vars.input.expectedRevision).toBe(2);
    expect(c.open()).toBe(false);
    expect(c.apiKey).toBe('');
  });
  it('copies deployment credentials only to the same endpoint and never reads a key', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.copy(settings.deploymentDefaults[0]);
    expect(c.apiKey).toBe('');
    mutate.mockResolvedValue(
      ok({
        testAIConnection: {
          result: {
            succeeded: true,
            message: 'Accepted',
            testedAt: '2026-11-06T00:00:00Z',
            visionRequest: false,
          },
          problems: [],
        },
      }),
    );
    await c.test();
    expect(mutate.mock.calls[0][1].input.draft.copyDeploymentKeyFrom).toBe('GARDEN');
    c.draft.endpoint = 'https://different.example/v1';
    await c.test();
    expect(mutate.mock.calls[1][1].input.draft.copyDeploymentKeyFrom).toBeUndefined();
    c.apiKey = 'replacement';
    await c.test();
    expect(mutate.mock.calls[2][1].input.draft.apiKey).toBe('replacement');
    expect(c.open()).toBe(true);
    c.close();
    expect(c.apiKey).toBe('');
  });
  it('preserves drafts on conflicts and reuses request identity for a retry', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.edit(connection);
    c.draft.name = 'Updated';
    c.apiKey = 'fixture-key';
    mutate.mockResolvedValue(
      ok({
        saveAIConnection: {
          connection: null,
          problems: [{ message: 'These AI settings changed. Reload them before saving.' }],
        },
      }),
    );
    await c.save();
    expect(c.formError()).toContain('Reload');
    expect(c.open()).toBe(true);
    expect(c.apiKey).toBe('fixture-key');
    await c.save();
    expect(mutate.mock.calls[0][1].clientMutationId).toBe(mutate.mock.calls[1][1].clientMutationId);
    c.draft.name = 'Another change';
    await c.save();
    expect(mutate.mock.calls[2][1].clientMutationId).not.toBe(
      mutate.mock.calls[0][1].clientMutationId,
    );
  });
  it('saves all feature choices with revisions and explicit off/default modes', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.assignments();
    c.choices.GARDEN = connection.id;
    c.choices.VISION = 'DISABLED';
    mutate.mockResolvedValue(ok({ saveAIFeatureBindings: { settings, problems: [] } }));
    await c.saveAssignments();
    expect(mutate.mock.calls[0][1].input).toEqual([
      { feature: 'GARDEN', mode: 'CONNECTION', connectionId: connection.id, expectedRevision: 0 },
      { feature: 'VISION', mode: 'DISABLED', connectionId: null, expectedRevision: 0 },
      { feature: 'RECEIPT', mode: 'DEFAULT', connectionId: null, expectedRevision: 0 },
    ]);
    expect(c.assignmentsOpen()).toBe(false);
  });
  it('shows a failed test and never saves as a side effect', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.edit(connection);
    mutate.mockResolvedValue(
      ok({
        testAIConnection: {
          result: {
            succeeded: false,
            message: 'Authentication rejected',
            testedAt: '2026-11-06T00:00:00Z',
            visionRequest: false,
          },
          problems: [],
        },
      }),
    );
    await c.test();
    expect(c.result()?.succeeded).toBe(false);
    expect(c.result()?.message).toBe('Authentication rejected');
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(c.open()).toBe(true);
  });
  it('applies deletion response, including disabled bindings, without choosing a replacement', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.deleting.set(connection);
    const after = {
      ...settings,
      connections: [],
      bindings: [{ feature: 'GARDEN', mode: 'DISABLED', connectionId: null, revision: 2 }],
    };
    mutate.mockResolvedValue(ok({ deleteAIConnection: { settings: after, problems: [] } }));
    await c.remove();
    expect(c.data()?.bindings[0].mode).toBe('DISABLED');
    expect(c.deleting()).toBeNull();
    expect(c.bindingLabel('GARDEN')).toBe('Off');
  });
});
