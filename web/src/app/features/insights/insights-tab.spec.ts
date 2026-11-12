import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { InsightsTab } from './insights-tab';

const caps = {
  providerCapabilities: {
    __typename: 'ProviderCapabilities' as const,
    text: {
      __typename: 'ProviderCapability' as const,
      available: true,
      model: 'stub-1',
      externalData: true,
      unavailableReason: null,
    },
    vision: {
      __typename: 'ProviderCapability' as const,
      available: false,
      model: null,
      externalData: false,
      unavailableReason: 'No vision provider is configured.',
    },
    identification: {
      __typename: 'ProviderCapability' as const,
      available: false,
      model: null,
      externalData: false,
      unavailableReason: 'No identification service is configured.',
    },
    receiptExtraction: {
      __typename: 'ProviderCapability' as const,
      available: true,
      model: null,
      externalData: false,
      unavailableReason: null,
    },
    budget: {
      __typename: 'AnalysisBudgetStatus' as const,
      dailyRunsUsed: 0,
      dailyRunLimit: 5,
      concurrentRunLimit: 1,
      monthUtc: '2026-11',
      monthCeilingMicroUsd: '10000000',
      monthReservedMicroUsd: '100000',
      monthSpentMicroUsd: '0',
    },
  },
};

const evidence = {
  activityIds: ['ac-1'],
  measurementIds: [],
  photoIds: ['ph-1', 'ph-2'],
  sourceVersionIds: ['sv-1'],
  weatherSnapshotIds: [],
  missingFacts: ['no humidity readings'],
  inputHash: 'aabbccddeeff0011',
};

function run(over: Record<string, unknown> = {}) {
  return {
    __typename: 'AnalysisRun' as const,
    id: 'rn-1',
    revision: 1,
    kind: 'SYNTHESIS',
    plantId: 'pl-1',
    receiptAssetId: null,
    state: 'QUEUED',
    attempts: 0,
    requestedAt: '2026-11-02T10:00:00Z',
    startedAt: null,
    finishedAt: null,
    failureCode: null,
    failureMessage: null,
    provider: null,
    model: null,
    promptVersion: 'obs-v3',
    resultSchemaVersion: 'obs-1',
    costReservedMicroUsd: '100000',
    costActualMicroUsd: null,
    evidence,
    result: null,
    suggestions: [],
    ...over,
  };
}

const succeeded = run({
  state: 'SUCCEEDED',
  attempts: 1,
  provider: 'stub',
  model: 'stub-1',
  costActualMicroUsd: '80000',
  result: {
    __typename: 'ObservationAnalysisResult' as const,
    visibleObservations: [
      {
        text: 'Two leaf tips show dry browning.',
        evidenceIds: ['ac-1', 'ph-1'],
      },
      { text: 'Substrate looks dry at the surface.', evidenceIds: [] },
    ],
    hypotheses: [
      {
        text: 'Low humidity may correlate with the browning.',
        confidence: 'MEDIUM',
        evidenceIds: ['ac-1'],
      },
    ],
    missingInformation: ['No humidity readings are recorded.'],
    followUpChecks: ['Check the leaf tips again in 7 days.'],
    citations: [
      {
        sourceId: 'src-1',
        sourceVersionId: 'sv-1',
        title: 'Houseplant humidity guide',
        publisher: 'Extension service',
        retrievedOn: '2026-10-01',
        region: 'temperate',
        license: 'CC-BY',
      },
    ],
  },
});

function stubFetch(handlers: Record<string, unknown>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as {
        query: string;
        variables?: object;
      };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      const vars = (body.variables ?? {}) as Record<string, unknown>;
      calls.push({ op, vars });
      let h = handlers[op];
      if (typeof h === 'function') h = await (h as (v: Record<string, unknown>) => unknown)(vars);
      if (h === undefined) h = {};
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('InsightsTab', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1',
      expiresAt: '2026-12-01T00:00:00Z',
      csrfToken: 'c',
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  function render(handlers: Record<string, unknown>, photos: unknown[] = []) {
    stubFetch({
      ProviderCapabilities: caps,
      SuggestionsPage: {
        suggestions: {
          items: [],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
      ...handlers,
    });
    const fixture = TestBed.createComponent(InsightsTab);
    fixture.componentRef.setInput('plantId', 'pl-1');
    fixture.componentRef.setInput('photos', photos);
    fixture.detectChanges();
    return fixture;
  }

  const dialog = (): HTMLElement => {
    const panels = [...document.querySelectorAll<HTMLElement>('.dialog-panel')];
    expect(panels, 'exactly one insights entry dialog is open').toHaveLength(1);
    return panels[0];
  };
  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    (fixture.nativeElement.textContent ?? '') +
    [...document.querySelectorAll('.dialog-panel')]
      .map((panel) => panel.textContent ?? '')
      .join(' ');
  const button = (root: HTMLElement, label: string) =>
    [...root.querySelectorAll('button')].find((b) => b.textContent?.trim() === label);

  const startReview = (fixture: { nativeElement: HTMLElement; detectChanges(): void }) => {
    button(fixture.nativeElement, 'Review this plant’s history')!.click();
    fixture.detectChanges();
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation RequestAnalysis'),
      ),
    ).toBe(false);
    button(dialog(), 'Start review')!.click();
  };

  it('states engine availability and reasons honestly', async () => {
    const fixture = render({});
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain('available · stub-1');
    expect(text).toContain('No vision provider is configured.');
    expect(text).toContain('No identification service is configured.');
    expect(text).toContain('0 of 5 runs today');
    expect(text).toContain('External engines receive the snapshot');
  });

  it('disables requests once the daily run budget is spent', async () => {
    const spent = structuredClone(caps);
    spent.providerCapabilities.budget.dailyRunsUsed = 5;
    const fixture = render({ ProviderCapabilities: spent });
    await flush();
    fixture.detectChanges();
    const ask = button(fixture.nativeElement, 'Review this plant’s history');
    expect(ask?.disabled).toBe(true);
    expect(textOf(fixture)).toContain('Daily analysis limit reached');
  });

  it('follows an existing run on IDEMPOTENCY_CONFLICT instead of failing', async () => {
    const fixture = render({
      RequestAnalysis: {
        requestAnalysis: {
          __typename: 'RequestAnalysisPayload',
          run: succeeded,
          problems: [
            {
              __typename: 'MutationProblem' as const,
              code: 'IDEMPOTENCY_CONFLICT',
              field: null,
              message: 'an identical run already exists; force a rerun to re-bill',
              currentRevision: null,
            },
          ],
        },
      },
    });
    await flush();
    fixture.detectChanges();
    startReview(fixture);
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('force a rerun to re-bill');
    expect(textOf(fixture)).toContain('Done');
    expect(textOf(fixture)).toContain('Two leaf tips show dry browning.');
    // evidence chips classify ids against the run's evidence block, and a
    // claim with no ids is called out as unsupported
    expect(textOf(fixture)).toContain('evidence: activity');
    expect(textOf(fixture)).toContain('no evidence referenced — treat as unsupported');
    // hypotheses are labelled as hypotheses with their confidence
    expect(textOf(fixture)).toContain('not diagnoses');
    expect(textOf(fixture)).toContain('medium confidence');
    // citations render as a source sheet
    expect(textOf(fixture)).toContain('Houseplant humidity guide — Extension service');
    expect(textOf(fixture)).toContain('CC-BY');
    // provenance line names the engine and the input hash
    expect(textOf(fixture)).toMatch(/Engine:\s+stub\s+·\s+stub-1/);
    expect(textOf(fixture)).toContain('prompt obs-v3 · input aabbccddeeff');
  });

  it('polls a queued run per contract (2s, then backing off) and stops on terminal state', async () => {
    vi.useFakeTimers();
    let statusCount = 0;
    const fixture = render({
      RequestAnalysis: {
        requestAnalysis: {
          __typename: 'RequestAnalysisPayload',
          run: run(),
          problems: [],
        },
      },
      AnalysisRunStatus: () => {
        statusCount += 1;
        return {
          analysisRun: statusCount >= 2 ? succeeded : run({ state: 'RUNNING', attempts: 1 }),
        };
      },
    });
    await vi.advanceTimersByTimeAsync(0);
    fixture.detectChanges();
    startReview(fixture);
    await vi.advanceTimersByTimeAsync(0);
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Queued — waiting for a worker');
    await vi.advanceTimersByTimeAsync(2000); // first poll at 2s
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Running');
    await vi.advanceTimersByTimeAsync(4000); // second poll at 4s
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Done');
    expect(statusCount).toBe(2);
    await vi.advanceTimersByTimeAsync(20000); // terminal: no further polls
    expect(statusCount).toBe(2);
  });

  it('cancels a live run at its current revision', async () => {
    const fixture = render({
      RequestAnalysis: {
        requestAnalysis: {
          __typename: 'RequestAnalysisPayload',
          run: run(),
          problems: [],
        },
      },
      CancelAnalysis: {
        cancelAnalysis: {
          __typename: 'CancelAnalysisPayload',
          run: run({
            state: 'CANCELLED',
            failureCode: null,
            failureMessage: null,
          }),
          problems: [],
        },
      },
    });
    await flush();
    fixture.detectChanges();
    startReview(fixture);
    await flush();
    fixture.detectChanges();
    button(fixture.nativeElement, 'Cancel')!.click();
    await flush();
    fixture.detectChanges();
    const cancelCall = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map((c) => JSON.parse(String(c[1].body)))
      .find((b) => b.query.includes('CancelAnalysis'));
    expect(cancelCall.variables.input.expectedRevision).toBe(1);
    expect(textOf(fixture)).toContain('Cancelled');
  });

  it('keeps the photo pair picker shut while vision is unavailable', async () => {
    const fixture = render({}, [
      { id: 'ph-1', takenAt: '2026-10-01T10:00:00Z', caption: null },
      { id: 'ph-2', takenAt: '2026-10-20T10:00:00Z', caption: null },
    ]);
    await flush();
    fixture.detectChanges();
    const compare = button(fixture.nativeElement, 'Compare growth in two photos');
    expect(compare?.disabled).toBe(true);
    compare!.click();
    fixture.detectChanges();
    expect(document.querySelectorAll('.dialog-panel')).toHaveLength(0);
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation RequestAnalysis'),
      ),
    ).toBe(false);
  });
});
