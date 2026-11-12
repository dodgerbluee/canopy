import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import { InsightSuggestionCard } from './suggestion-card';

const sugg = {
  __typename: 'Suggestion' as const,
  id: 'sg-1',
  revision: 1,
  plantId: 'pl-1',
  runId: 'rn-1',
  kind: 'CARE' as const,
  severity: 'MODERATE' as const,
  status: 'PROPOSED' as const,
  observation: 'Leaf tips show browning.',
  hypothesis: 'The air around the pot may be dry.',
  action: 'Check the leaf tips again in 7 days.',
  evidenceIds: ['ph-1', 'ph-2'],
  missingFacts: ['No humidity readings are recorded.'],
  createdAt: '2026-11-01T10:00:00Z',
  expiresAt: '2026-11-15T10:00:00Z',
  needsReview: false,
  acceptedTaskId: null,
  dismissReason: null,
};

const evidence = {
  activityIds: [],
  measurementIds: [],
  photoIds: ['ph-1', 'ph-2'],
  sourceVersionIds: [],
  weatherSnapshotIds: [],
  missingFacts: [],
  inputHash: 'deadbeef',
};

const problem = (code: string, message: string, currentRevision: number | null = null) => ({
  __typename: 'MutationProblem' as const,
  code,
  field: null,
  message,
  currentRevision,
});

const occurrence = {
  __typename: 'CareOccurrence' as const,
  id: 'oc-1',
  revision: 1,
  planId: 'cp-1',
  plantId: 'pl-1',
  kind: 'CHECK' as const,
  currentDueDate: '2026-11-05',
  scheduledFor: '2026-11-05',
  snoozedUntil: null,
  completedAt: null,
  state: 'OPEN' as const,
};

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
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('InsightSuggestionCard', () => {
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

  afterEach(() => vi.unstubAllGlobals());

  function render(overrides: Partial<typeof sugg> = {}, handlers: Record<string, unknown> = {}) {
    stubFetch(handlers);
    const fixture = TestBed.createComponent(InsightSuggestionCard);
    fixture.componentRef.setInput('suggestion', { ...sugg, ...overrides });
    fixture.componentRef.setInput('evidence', evidence);
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
  const toastText = () =>
    TestBed.inject(UiToasts)
      .toasts()
      .map((t) => t.message)
      .join(' | ');

  it('shows the structured parts and links a two-photo evidence pair to compare', () => {
    const fixture = render();
    const text = textOf(fixture);
    expect(text).toContain('Check the leaf tips again in 7 days.');
    expect(text).toContain('Leaf tips show browning.');
    expect(text).toContain('Possible explanation');
    expect(text).toContain('No humidity readings are recorded.');
    const links = [...fixture.nativeElement.querySelectorAll('.evidence-summary a')];
    expect(links.map((a) => a.getAttribute('href'))).toEqual([
      '/plants/pl-1/compare?a=ph-1&b=ph-2',
    ]);
  });

  it('keeps context collapsed and summarizes supporting photos once', () => {
    const fixture = render();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.querySelector<HTMLDetailsElement>('details')?.open).toBe(false);
    expect(root.querySelectorAll('.evidence-summary a')).toHaveLength(1);
    expect(root.querySelector('.evidence-summary')?.textContent).toContain('2 photos');
    expect(root.textContent).not.toContain('evidence:');
    expect(root.textContent).not.toContain('Accepting adds one check task');
  });

  it('formats explicit money amounts without changing the stored proposal', () => {
    const fixture = render({
      observation: 'Allocated total 13900 USD minor units across 1 purchase.',
      action: 'Review costs.',
    });
    expect(fixture.nativeElement.textContent).toContain('139.00 USD');
    expect(fixture.nativeElement.textContent).not.toContain('13900 USD minor units');
    expect(fixture.componentInstance.suggestion().observation).toContain('13900 USD minor units');
  });

  it('labels a suggestion without evidence records as unsupported', () => {
    const fixture = render({ evidenceIds: [] });
    expect(textOf(fixture)).toContain('treat it as unsupported');
  });

  it('accept previews one check task and reports the created occurrence', async () => {
    const fixture = render(
      {},
      {
        AcceptSuggestion: {
          acceptSuggestion: {
            __typename: 'AcceptSuggestionPayload',
            suggestion: { ...sugg, status: 'ACCEPTED', acceptedTaskId: 'oc-1' },
            occurrence,
            problems: [],
          },
        },
      },
    );
    const changed = vi.fn();
    fixture.componentInstance.changed.subscribe(changed);
    button(fixture.nativeElement, 'Add to care')!.click();
    fixture.detectChanges();
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation AcceptSuggestion'),
      ),
    ).toBe(false);
    button(dialog(), 'Add check')!.click();
    await flush();
    expect(toastText()).toContain('Task added to care — due 2026-11-05');
    expect(changed).toHaveBeenCalled();
  });

  it('REVIEW_REQUIRED becomes a review-then-confirm step at the server revision', async () => {
    let acceptCount = 0;
    const fixture = render(
      { needsReview: true },
      {
        AcceptSuggestion: () => {
          acceptCount += 1;
          if (acceptCount === 1)
            return {
              acceptSuggestion: {
                __typename: 'AcceptSuggestionPayload',
                suggestion: { ...sugg, revision: 2, needsReview: false },
                occurrence: null,
                problems: [
                  problem(
                    'VALIDATION',
                    'REVIEW_REQUIRED: a source or the plant context changed; review the evidence, then accept at the current revision.',
                    2,
                  ),
                ],
              },
            };
          return {
            acceptSuggestion: {
              __typename: 'AcceptSuggestionPayload',
              suggestion: {
                ...sugg,
                revision: 3,
                status: 'ACCEPTED',
                acceptedTaskId: 'oc-1',
              },
              occurrence,
              problems: [],
            },
          };
        },
      },
    );
    button(fixture.nativeElement, 'Add to care')!.click();
    fixture.detectChanges();
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation AcceptSuggestion'),
      ),
    ).toBe(false);
    button(dialog(), 'Add check')!.click();
    await flush();
    fixture.detectChanges();
    expect(dialog().textContent).toContain('Your review was recorded');
    expect(dialog().textContent).toContain('Current revision: 2');
    const confirm = button(dialog(), 'Confirm accept');
    expect(confirm).toBeTruthy();
    confirm!.click();
    await flush();
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) =>
      JSON.parse(String(c[1].body)),
    );
    const second = calls.filter((c) => c.query.includes('AcceptSuggestion')).at(-1);
    expect(second.variables.input.expectedRevision).toBe(2);
    expect(toastText()).toContain('Task added to care');
  });

  it('a duplicate accept shows the server message and re-reads', async () => {
    const fixture = render(
      {},
      {
        AcceptSuggestion: {
          acceptSuggestion: {
            __typename: 'AcceptSuggestionPayload',
            suggestion: { ...sugg, status: 'ACCEPTED', acceptedTaskId: 'oc-9' },
            occurrence: null,
            problems: [
              problem('CONFLICT', 'suggestion already accepted; its task already exists.', 2),
            ],
          },
        },
      },
    );
    const changed = vi.fn();
    fixture.componentInstance.changed.subscribe(changed);
    button(fixture.nativeElement, 'Add to care')!.click();
    fixture.detectChanges();
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation AcceptSuggestion'),
      ),
    ).toBe(false);
    button(dialog(), 'Add check')!.click();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('already accepted');
    expect(changed).toHaveBeenCalled();
  });

  it('dismiss is final and re-reads the list', async () => {
    const fixture = render(
      {},
      {
        DismissSuggestion: {
          dismissSuggestion: {
            __typename: 'DismissSuggestionPayload',
            suggestion: { ...sugg, status: 'DISMISSED' },
            problems: [],
          },
        },
      },
    );
    const changed = vi.fn();
    fixture.componentInstance.changed.subscribe(changed);
    button(fixture.nativeElement, 'Dismiss')!.click();
    fixture.detectChanges();
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation DismissSuggestion'),
      ),
    ).toBe(false);
    button(dialog(), 'Dismiss')!.click();
    await flush();
    expect(toastText()).toContain('Suggestion dismissed');
    expect(changed).toHaveBeenCalled();
  });
});
