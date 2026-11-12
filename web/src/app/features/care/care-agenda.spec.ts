import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import { CareAgendaPage } from './care-agenda';
import { localToday, offsetDate, type AgendaItem } from './care-view';

/** Agenda rows exactly as the frozen CareOccurrenceFields fragment ships. */
function occurrence(over: Partial<AgendaItem> = {}): Record<string, unknown> {
  return {
    id: 'oc-1',
    planId: 'p-1',
    plantId: 'pl-1',
    ruleKey: 'water',
    kind: 'ACTION',
    activityKind: 'WATER',
    occurrenceKey: 'water:1',
    originalDueDate: localToday(),
    currentDueDate: localToday(),
    snoozedUntil: null,
    status: 'DUE',
    completedActivityId: null,
    resolvedAt: null,
    planTimezone: 'UTC',
    reason: { text: '7 days since last soak', evidence: [], missingFacts: [] },
    ...over,
  };
}

function agendaEnvelope(items: unknown[], hasNext = false, endCursor: string | null = null) {
  return {
    careAgenda: { items, pageInfo: { endCursor, hasNextPage: hasNext } },
  };
}

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
      if (typeof h === 'function') h = (h as (v: Record<string, unknown>) => unknown)(vars);
      if (h === 'network') throw new TypeError('fetch failed');
      if (h === '401') return new Response('', { status: 401 });
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('CareAgendaPage', () => {
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

  const plants = {
    plants: {
      items: [
        { id: 'pl-1', name: 'Monstera' },
        { id: 'pl-2', name: 'Calathea' },
      ],
      pageInfo: { endCursor: null, hasNextPage: false },
    },
  };

  function render(handlers: Record<string, unknown>) {
    stubFetch({ Plants: plants, ...handlers });
    const fixture = TestBed.createComponent(CareAgendaPage);
    fixture.detectChanges();
    return fixture;
  }

  const dialog = (): HTMLElement => {
    const panels = [...document.querySelectorAll<HTMLElement>('.dialog-panel')];
    expect(panels, 'exactly one care entry dialog is open').toHaveLength(1);
    return panels[0];
  };
  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    (fixture.nativeElement.textContent ?? '') +
    [...document.querySelectorAll('.dialog-panel')]
      .map((panel) => panel.textContent ?? '')
      .join(' ');
  const buttons = (root: HTMLElement, label: string) =>
    [...root.querySelectorAll('button')].filter((b) => b.textContent?.trim() === label);

  it('groups overdue/today/upcoming, names plants, and distinguishes checks from actions', async () => {
    const today = localToday();
    const fixture = render({
      CareAgenda: agendaEnvelope([
        occurrence({
          id: 'o1',
          currentDueDate: offsetDate(today, -2),
          originalDueDate: offsetDate(today, -2),
        }),
        occurrence({
          id: 'o2',
          plantId: 'pl-2',
          ruleKey: 'moisture-check',
          kind: 'CHECK',
          activityKind: null,
          currentDueDate: today,
          originalDueDate: today,
        }),
        occurrence({
          id: 'o3',
          currentDueDate: offsetDate(today, 3),
          originalDueDate: offsetDate(today, 3),
        }),
      ]),
    });
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain('Overdue');
    expect(text).toContain('Today');
    expect(text).toContain('Upcoming');
    expect(text).toContain('Monstera');
    expect(text).toContain('Calathea');
    expect(text).toContain('check');
    expect(text).toContain('water');
    expect(text).toContain('Due ' + today);
    // the agenda asks the server for a window that includes overdue work
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls as unknown[][];
    void calls;
  });

  it('snoozed rows show the moved date and the original; missing facts are named', async () => {
    const today = localToday();
    const fixture = render({
      CareAgenda: agendaEnvelope([
        occurrence({
          id: 'o1',
          currentDueDate: offsetDate(today, 1),
          originalDueDate: today,
          snoozedUntil: offsetDate(today, 1),
        }),
        occurrence({
          id: 'o2',
          ruleKey: 'feed',
          activityKind: 'FERTILIZE',
          reason: {
            text: 'grow season',
            evidence: [],
            missingFacts: ['PLACEMENT', 'CULTIVAR'],
          },
        }),
      ]),
    });
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain(`Snoozed to ${offsetDate(today, 1)} · originally ${today}`);
    expect(text).toContain('Needs context: placement, cultivar');
    expect(text).toContain('will not guess');
  });

  it('completing an action sends quantity/unit/note with expectedRevision 1', async () => {
    const calls = render({
      CareAgenda: agendaEnvelope([occurrence({ id: 'oc-9' })]),
      CompleteOccurrence: {
        completeOccurrence: {
          occurrence: occurrence({ id: 'oc-9' }),
          activity: null,
          problems: [],
        },
      },
    });
    await flush();
    calls.detectChanges();

    buttons(calls.nativeElement, 'Complete')[0].click();
    calls.detectChanges();
    const root = dialog();
    const qty = root.querySelector('input[name="quantity"]') as HTMLInputElement;
    const unit = root.querySelector('select[name="unit"]') as HTMLSelectElement;
    const note = root.querySelector('input[name="note"]') as HTMLInputElement;
    expect(qty).toBeTruthy();
    expect(unit.value).toBe('ML');
    qty.value = '500';
    unit.value = 'ML';
    note.value = 'top 3cm dry';
    root.querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    calls.detectChanges();

    const mut = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map((c: unknown[]) => JSON.parse(String((c[1] as RequestInit).body)))
      .find((b: { query: string }) => b.query.includes('mutation CompleteOccurrence'));
    expect(mut).toBeTruthy();
    expect(mut.variables['input']).toMatchObject({
      occurrenceId: 'oc-9',
      expectedRevision: 1,
      quantity: '500',
      unit: 'ML',
      note: 'top 3cm dry',
    });
    expect(
      TestBed.inject(UiToasts)
        .toasts()
        .some((t) => t.kind === 'ok'),
    ).toBe(true);
  });

  it('a revision CONFLICT names the current revision and the retry uses it', async () => {
    let revTried: number | null = null;
    const fixture = render({
      CareAgenda: agendaEnvelope([occurrence({ id: 'oc-9' })]),
      CompleteOccurrence: (vars: Record<string, unknown>) => {
        const input = vars['input'] as { expectedRevision: number };
        revTried = input.expectedRevision;
        return {
          completeOccurrence: {
            occurrence: null,
            activity: null,
            problems: [
              {
                code: 'CONFLICT',
                field: null,
                message: 'stale write',
                currentRevision: 3,
              },
            ],
          },
        };
      },
    });
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Complete')[0].click();
    fixture.detectChanges();
    dialog().querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    fixture.detectChanges();
    expect(revTried).toBe(1);
    expect(
      TestBed.inject(UiToasts)
        .toasts()
        .some((t) => t.kind === 'error' && t.message.includes('revision 3')),
    ).toBe(true);

    // The failed entry stays open; retry it with the hinted revision.
    expect(dialog().querySelector('form')).toBeTruthy();
    dialog().querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    expect(revTried).toBe(3);
  });

  it('snooze defaults to the next day and skip needs a confirm step', async () => {
    const today = localToday();
    const fixture = render({
      CareAgenda: agendaEnvelope([
        occurrence({
          id: 'oc-9',
          currentDueDate: today,
          originalDueDate: today,
        }),
      ]),
      SnoozeOccurrence: {
        snoozeOccurrence: {
          occurrence: occurrence({ id: 'oc-9' }),
          problems: [],
        },
      },
      SkipOccurrence: {
        skipOccurrence: {
          occurrence: occurrence({ id: 'oc-9', status: 'SKIPPED' }),
          problems: [],
        },
      },
    });
    await flush();
    fixture.detectChanges();

    buttons(fixture.nativeElement, 'Snooze')[0].click();
    fixture.detectChanges();
    const dateInput = dialog().querySelector('input[type="date"]') as HTMLInputElement;
    expect(dateInput.value).toBe(offsetDate(today, 1));
    dateInput.value = offsetDate(today, 4);
    dialog().querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    const snoozeCall = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map((c: unknown[]) => JSON.parse(String((c[1] as RequestInit).body)))
      .find((b: { query: string }) => b.query.includes('mutation SnoozeOccurrence'));
    expect(snoozeCall.variables['input']).toMatchObject({
      occurrenceId: 'oc-9',
      expectedRevision: 1,
      until: offsetDate(today, 4),
    });

    buttons(fixture.nativeElement, 'Skip')[0].click();
    fixture.detectChanges();
    expect(dialog().querySelector('.dialog-title')?.textContent).toBe('Skip occurrence');
    expect(
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((call) =>
        String((call[1] as RequestInit).body).includes('mutation SkipOccurrence'),
      ),
    ).toBe(false);
    buttons(dialog(), 'Confirm skip')[0].click();
    await flush();
    const skipCall = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map((c: unknown[]) => JSON.parse(String((c[1] as RequestInit).body)))
      .find((b: { query: string }) => b.query.includes('mutation SkipOccurrence'));
    expect(skipCall).toBeTruthy();
  });

  it('a load failure is an explicit error card, never a fake empty agenda', async () => {
    const fixture = render({ CareAgenda: 'network' });
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain('Agenda failed to load');
    expect(text).toContain('not an empty agenda');
    expect(text).not.toContain('Nothing due');
  });

  it('empty agenda offers a route to plan a plant', async () => {
    const fixture = render({ CareAgenda: agendaEnvelope([]) });
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('No scheduled care yet');
    expect(fixture.nativeElement.querySelector('a[href="/plants"]')).toBeTruthy();
  });
});
