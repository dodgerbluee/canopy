import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { CareTab } from './care-tab';
import { localToday, type PlanFields } from './care-view';

function stubFetch(handlers: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as { query: string };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      const h = handlers[op];
      if (h === 'network') throw new TypeError('fetch failed');
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
}

const flush = () => new Promise((r) => setTimeout(r, 0));

function planFixture(): PlanFields {
  return {
    id: 'cp-1',
    plantId: 'pl-1',
    profileVersion: {
      id: 'pv-2',
      profileId: 'pr-1',
      version: 2,
      status: 'REVIEWED',
      reviewerLabel: null,
      reviewedOn: null,
      publishedAt: null,
      retiredAt: null,
      createdAt: '2026-02-02T00:00:00Z',
      environment: { situations: [], hemispheres: [] },
      rules: [
        {
          ruleKey: 'water',
          kind: 'ACTION',
          activityKind: 'WATER',
          recurrence: { __typename: 'EveryNDaysRecurrence', days: 7, anchor: 'PLAN_START' },
          requiredFacts: [],
          weatherSensitivity: 'NONE',
          guidanceText: 'Soak and drain.',
          sourceVersions: [
            {
              id: 'sv-1',
              sourceId: 'src-1',
              citation: 'Orchard Press, 2023',
              summary: null,
              retrievedOn: '2026-01-15',
              contentSha256: 'ab'.repeat(32),
              createdAt: '2026-01-15T00:00:00Z',
            },
          ],
        },
      ],
    },
    timezone: 'Europe/Berlin',
    enabled: true,
    notificationTime: '08:30',
    overrides: [],
    revision: 3,
    createdAt: '2026-09-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
  } as unknown as PlanFields;
}

describe('CareTab', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({ workspaceId: 'w1', expiresAt: '2026-12-01T00:00:00Z', csrfToken: 'c' });
  });

  afterEach(() => vi.unstubAllGlobals());

  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    fixture.nativeElement.textContent ?? '';

  it('no plan is an explicit empty state with a create route', async () => {
    stubFetch({ PlantCarePlan: { plant: { id: 'pl-1', carePlan: null } } });
    const fixture = TestBed.createComponent(CareTab);
    fixture.componentRef.setInput('plantId', 'pl-1');
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('No care plan yet');
    expect(fixture.nativeElement.querySelector('a[href="/care/plan/pl-1"]')).toBeTruthy();
  });

  it('shows the plan summary, cited rules, and the plant-scoped mini agenda', async () => {
    const today = localToday();
    stubFetch({
      PlantCarePlan: { plant: { id: 'pl-1', carePlan: planFixture() } },
      CareAgenda: {
        careAgenda: {
          items: [
            {
              id: 'oc-1',
              planId: 'cp-1',
              plantId: 'pl-1',
              ruleKey: 'water',
              kind: 'ACTION',
              activityKind: 'WATER',
              occurrenceKey: 'k',
              originalDueDate: today,
              currentDueDate: today,
              snoozedUntil: null,
              status: 'DUE',
              completedActivityId: null,
              resolvedAt: null,
              planTimezone: 'Europe/Berlin',
              reason: { text: 'due', evidence: [], missingFacts: ['PLACEMENT'] },
            },
            {
              id: 'oc-2',
              planId: 'cp-1',
              plantId: 'pl-1',
              ruleKey: 'inspect',
              kind: 'CHECK',
              activityKind: null,
              occurrenceKey: 'k2',
              originalDueDate: today,
              currentDueDate: today,
              snoozedUntil: null,
              status: 'DUE',
              completedActivityId: null,
              resolvedAt: null,
              planTimezone: 'Europe/Berlin',
              reason: null,
            },
          ],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
    });
    const fixture = TestBed.createComponent(CareTab);
    fixture.componentRef.setInput('plantId', 'pl-1');
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain('Enabled');
    expect(text).toContain('Europe/Berlin');
    expect(text).toContain('08:30');
    expect(text).toContain('rev 3');
    expect(text).toContain('Every 7 days (from plan start)');
    expect(text).toContain('Orchard Press, 2023');
    expect(text).toContain('Today');
    expect(text).toContain('check');
    expect(text).toContain('Needs context: placement');
    // the mini agenda is scoped to this plant
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls as [string, RequestInit][];
    const agendaCall = calls.find(([, i]) => String(i.body).includes('query CareAgenda'))!;
    const body = JSON.parse(String(agendaCall[1].body)) as {
      variables: Record<string, unknown>;
    };
    expect(body.variables['plantId']).toBe('pl-1');
  });
});
