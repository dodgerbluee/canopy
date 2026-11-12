import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import { TodayPage } from './today';
import { localToday, type AgendaItem } from '../care/care-view';

/** Due rows exactly as the frozen CareOccurrenceFields fragment ships. */
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
    reason: { text: 'Daily soak', evidence: [], missingFacts: [] },
    ...over,
  };
}

function overview(due: unknown[] = [], entries: unknown[] = [], conditions: unknown | null = null) {
  return {
    todayOverview: {
      dueTasks: {
        items: due,
        pageInfo: { endCursor: null, hasNextPage: false },
      },
      recentEntries: {
        items: entries,
        pageInfo: { endCursor: null, hasNextPage: false },
      },
      conditions,
    },
  };
}

const activityEntry = {
  __typename: 'ActivityEntry',
  id: 'je-1',
  plantId: 'pl-1',
  occurredAt: '2026-11-02T09:00:00Z',
  summary: 'Watered',
  activity: {
    id: 'a-1',
    plantId: 'pl-1',
    kind: 'WATER',
    occurredAt: '2026-11-02T09:00:00Z',
    timezone: 'UTC',
    note: null,
    quantity: '500',
    unit: 'ML',
    product: null,
    revision: 1,
    voidedAt: null,
  },
};

const conditionsFresh = {
  locationName: 'South yard',
  snapshot: {
    id: 'ws-1',
    provider: 'open-meteo',
    fetchedAt: '2026-11-02T09:00:00Z',
    validFrom: '2026-11-02T00:00:00Z',
    validUntil: '2026-11-03T00:00:00Z',
    highTempC: '23.5',
    lowTempC: '11',
    rainfallMm: '18.4',
    et0Mm: '3.1',
    freshness: 'FRESH',
    license: 'Open-Meteo terms',
  },
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

describe('TodayPage', () => {
  beforeEach(() => {
    sessionStorage.clear();
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

  const plants = (items: unknown[] = [{ id: 'pl-1', name: 'Monstera' }]) => ({
    plants: { items, pageInfo: { endCursor: null, hasNextPage: false } },
  });

  function render(handlers: Record<string, unknown>) {
    stubFetch({ Plants: plants(), TodayOverview: overview(), ...handlers });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    return fixture;
  }

  const dialog = (): HTMLElement => {
    const panels = [...document.querySelectorAll<HTMLElement>('.dialog-panel')];
    expect(panels, 'exactly one care entry dialog is open').toHaveLength(1);
    return panels[0];
  };
  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    fixture.nativeElement.textContent ?? '';
  const button = (root: HTMLElement, label: string) =>
    [...root.querySelectorAll('button')].find((b) => b.textContent?.trim() === label);
  const toastText = () =>
    TestBed.inject(UiToasts)
      .toasts()
      .map((t) => t.message)
      .join(' | ');

  const coverPlant = (id: string, name: string, category = 'FIG', hasCover = true) => ({
    id,
    name,
    category,
    type: 'INDIVIDUAL',
    cultivar: null,
    photoCount: hasCover ? 3 : 0,
    currentContext: null,
    coverPhoto: hasCover ? { asset: { id: 'photo-' + id } } : null,
  });

  it('changes the featured cover on each visit and uses the real plant name as headline', async () => {
    const rows = [
      coverPlant('bare', 'No cover', 'CITRUS', false),
      coverPlant('fig', 'Alma Fig'),
      coverPlant('lime', 'Lime', 'CITRUS'),
    ];
    const titles: string[] = [];
    for (let visit = 0; visit < 3; visit++) {
      const fixture = render({ Plants: plants(rows) });
      await flush();
      fixture.detectChanges();
      titles.push(fixture.nativeElement.querySelector('#hero-title').textContent.trim());
      fixture.destroy();
    }
    expect(titles).toEqual(['Alma Fig', 'Lime', 'Alma Fig']);
  });

  it('retains an honest placeholder when no plant has an uploaded cover', async () => {
    const fixture = render({ Plants: plants([coverPlant('fig', 'Alma Fig', 'FIG', false)]) });
    await flush();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('#hero-title').textContent.trim()).toBe('Alma Fig');
    expect(fixture.nativeElement.querySelector('.hero-photo img')).toBeNull();
  });

  it('one composition on load: overview + plant names + the suggestions slot, nothing else', async () => {
    const calls = stubFetch({
      Plants: plants(),
      TodayOverview: overview([occurrence()]),
    });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    // T34 added the two supplementary reads behind the suggestions slot
    expect(calls.map((c) => c.op).sort()).toEqual([
      'Plants',
      'ProviderCapabilities',
      'SuggestionsPage',
      'TodayOverview',
    ]);
    expect(textOf(fixture)).toContain('Monstera');
    expect(textOf(fixture)).toContain('Daily soak');
  });

  it('zero provider mode: conditions null says no location, never a placeholder', async () => {
    const fixture = render({ TodayOverview: overview([occurrence()]) });
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('No location with coordinates yet');
    expect(textOf(fixture)).not.toContain('open-meteo');
  });

  it('renders conditions with location name and freshness band', async () => {
    const fixture = render({
      TodayOverview: overview([occurrence()], [], conditionsFresh),
    });
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('South yard');
    expect(textOf(fixture)).toContain('23.5');
    expect(textOf(fixture)).not.toContain('Stale');
  });

  it('stale snapshot is shown as stale, not fresh', async () => {
    const stale = {
      ...conditionsFresh,
      snapshot: { ...conditionsFresh.snapshot, freshness: 'STALE' },
    };
    const fixture = render({ TodayOverview: overview([], [], stale) });
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Stale');
  });

  it('empty garden offers the opt-in starter, never seeds silently', async () => {
    stubFetch({ Plants: plants([]), TodayOverview: overview() });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Start with a curated collection?');
    expect(button(fixture.nativeElement, 'Opt in to starter collection')).toBeTruthy();
  });

  it('quick-complete WATER demands a quantity — the log never guesses one', async () => {
    const fixture = render({ TodayOverview: overview([occurrence()]) });
    await flush();
    fixture.detectChanges();

    button(fixture.nativeElement, 'Complete')!.click();
    fixture.detectChanges();
    button(dialog(), 'Confirm')!.click();
    await flush();
    expect(toastText()).toContain('Enter a decimal quantity');

    const qty = dialog().querySelector('input[name="quantity"]') as HTMLInputElement;
    const unit = dialog().querySelector('select[name="unit"]') as HTMLSelectElement;
    qty.value = '500';
    unit.value = 'ML';
    button(dialog(), 'Confirm')!.click();
    await flush();
    expect(toastText()).toContain('Completed — logged to the journey.');
  });

  it('complete sends the frozen input shape and re-reads the whole composition', async () => {
    const calls = stubFetch({
      Plants: plants([coverPlant('pl-1', 'Meyer Lemon', 'CITRUS'), coverPlant('fig', 'Alma Fig')]),
      TodayOverview: vi
        .fn()
        .mockResolvedValueOnce(overview([occurrence()]))
        .mockResolvedValue(overview([], [activityEntry])),
      CompleteOccurrence: {
        completeOccurrence: {
          occurrence: { id: 'oc-1', status: 'COMPLETED' },
          activity: { id: 'a-1', kind: 'WATER' },
          problems: [],
        },
      },
    });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();

    const heroBefore = fixture.nativeElement.querySelector('#hero-title').textContent.trim();
    button(fixture.nativeElement, 'Complete')!.click();
    fixture.detectChanges();
    const qty = dialog().querySelector('input[name="quantity"]') as HTMLInputElement;
    const unit = dialog().querySelector('select[name="unit"]') as HTMLSelectElement;
    qty.value = '2.5';
    unit.value = 'L';
    button(dialog(), 'Confirm')!.click();
    await flush();
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('#hero-title').textContent.trim()).toBe(heroBefore);
    const mut = calls.find((c) => c.op === 'CompleteOccurrence');
    expect(mut?.vars).toMatchObject({
      input: {
        occurrenceId: 'oc-1',
        expectedRevision: 1,
        quantity: '2.5',
        unit: 'L',
      },
    });
    // the composition is re-read, not locally patched: journal line appears
    expect(textOf(fixture)).toContain('Watered');
    expect(calls.filter((c) => c.op === 'TodayOverview').length).toBe(2);
  });

  it('conflict syncs the revision and says nothing was saved', async () => {
    let attempts = 0;
    const calls = stubFetch({
      Plants: plants(),
      TodayOverview: overview([occurrence()]),
      CompleteOccurrence: () => {
        attempts++;
        return attempts === 1
          ? {
              completeOccurrence: {
                occurrence: null,
                activity: null,
                problems: [
                  {
                    code: 'CONFLICT',
                    field: null,
                    message: 'Changed elsewhere',
                    currentRevision: 3,
                  },
                ],
              },
            }
          : {
              completeOccurrence: {
                occurrence: { id: 'oc-1', status: 'COMPLETED' },
                activity: { id: 'a-1', kind: 'WATER' },
                problems: [],
              },
            };
      },
    });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();

    button(fixture.nativeElement, 'Complete')!.click();
    fixture.detectChanges();
    const qty = dialog().querySelector('input[name="quantity"]') as HTMLInputElement;
    const unit = dialog().querySelector('select[name="unit"]') as HTMLSelectElement;
    qty.value = '500';
    unit.value = 'ML';
    const complete = () => button(dialog(), 'Confirm')!.click();
    complete();
    await flush();
    fixture.detectChanges();
    expect(toastText()).toContain('another device');
    expect(dialog().contains(qty)).toBe(true);
    expect(qty.value).toBe('500');
    expect(unit.value).toBe('ML');

    complete();
    await flush();
    expect(toastText()).toContain('Completed — logged to the journey.');
    const muts = calls.filter((c) => c.op === 'CompleteOccurrence');
    expect(muts[1]?.vars).toMatchObject({ input: { expectedRevision: 3 } });
  });

  it('snooze moves the card and keeps the original date visible', async () => {
    const snoozed = occurrence({
      currentDueDate: '2099-01-05',
      snoozedUntil: '2099-01-05',
    });
    stubFetch({
      Plants: plants(),
      TodayOverview: vi
        .fn()
        .mockResolvedValueOnce(overview([occurrence()]))
        .mockResolvedValue(overview([snoozed])),
      SnoozeOccurrence: {
        snoozeOccurrence: { occurrence: snoozed, problems: [] },
      },
    });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();

    button(fixture.nativeElement, 'Snooze')!.click();
    fixture.detectChanges();
    const until = dialog().querySelector('input[name="until"]') as HTMLInputElement;
    until.value = '2099-01-05';
    button(dialog(), 'Confirm')!.click();
    await flush();
    fixture.detectChanges();
    expect(toastText()).toContain('Snoozed to 2099-01-05');
    expect(textOf(fixture)).toContain('Snoozed to 2099-01-05 · originally');
  });

  it('shows the server error without claiming a connection failure or an empty garden', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              errors: [{ message: 'The care data could not be read.' }],
            }),
            { status: 200, headers: { 'content-type': 'application/json' } },
          ),
      ),
    );
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('The care data could not be read.');
    expect(textOf(fixture)).not.toContain('Could not reach the server');
    expect(button(fixture.nativeElement, 'Opt in to starter collection')).toBeFalsy();
  });

  it('load failure is an error state, not a fake empty day', async () => {
    stubFetch({ Plants: plants(), TodayOverview: 'network' });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Today failed to load');
    expect(textOf(fixture)).toContain('not an empty day');
    expect(button(fixture.nativeElement, 'Retry')).toBeTruthy();
  });

  it('a failed Plants read is an error state, never a starter CTA or zero garden', async () => {
    stubFetch({ Plants: 'network', TodayOverview: overview([occurrence()]) });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Today failed to load');
    expect(textOf(fixture)).not.toContain('Start with a curated collection?');
    expect(button(fixture.nativeElement, 'Opt in to starter collection')).toBeFalsy();
  });

  it('401 marks the session expired instead of rendering an empty day', async () => {
    stubFetch({ Plants: plants(), TodayOverview: '401' });
    const fixture = TestBed.createComponent(TodayPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    expect(TestBed.inject(SessionService).state()).toBe('anon');
    expect(textOf(fixture)).not.toContain('Nothing due today');
  });

  it('the suggestions slot states it is empty — no invented activity', async () => {
    const fixture = render({ TodayOverview: overview([occurrence()]) });
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('Nothing to suggest yet');
  });

  it('T34: open suggestions render as cards with the engine state in the header', async () => {
    const fixture = render({
      TodayOverview: overview([occurrence()]),
      ProviderCapabilities: {
        providerCapabilities: {
          text: {
            available: true,
            model: 'stub-1',
            externalData: true,
            unavailableReason: null,
          },
          vision: {
            available: false,
            model: null,
            externalData: false,
            unavailableReason: 'none',
          },
          identification: {
            available: false,
            model: null,
            externalData: false,
            unavailableReason: 'none',
          },
          receiptExtraction: {
            available: true,
            model: null,
            externalData: false,
            unavailableReason: null,
          },
          budget: {
            dailyRunsUsed: 1,
            dailyRunLimit: 5,
            concurrentRunLimit: 1,
            monthUtc: '2026-11',
            monthCeilingMicroUsd: '10000000',
            monthReservedMicroUsd: '0',
            monthSpentMicroUsd: '0',
          },
        },
      },
      SuggestionsPage: {
        suggestions: {
          items: [
            {
              id: 'sg-1',
              revision: 1,
              plantId: 'pl-1',
              runId: 'rn-1',
              kind: 'CARE',
              severity: 'MODERATE',
              status: 'PROPOSED',
              observation: 'Leaf tips show browning.',
              hypothesis: null,
              action: 'Check the leaf tips again in 7 days.',
              evidenceIds: ['ac-1'],
              missingFacts: [],
              createdAt: '2026-11-01T10:00:00Z',
              expiresAt: '2026-11-15T10:00:00Z',
              needsReview: false,
              acceptedTaskId: null,
              dismissReason: null,
            },
          ],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
    });
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('1 open');
    expect(textOf(fixture)).toContain('Check the leaf tips again in 7 days.');
    // no run context on this page: evidence chips stay honest generic records
    expect(textOf(fixture)).toContain('evidence: record');
  });
});
