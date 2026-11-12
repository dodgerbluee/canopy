import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, Params, Router } from '@angular/router';
import { BehaviorSubject } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import { CollectionPage } from './collection';
import { STARTER_PLANTS, starterKey } from './starter';

/** Minimal PlantFields row as the server would return it. */
function plantRow(over: Record<string, unknown> = {}) {
  return {
    id: 'p1',
    name: 'Meyer lemon',
    scientificName: null,
    cultivar: null,
    rootstock: null,
    type: 'INDIVIDUAL',
    count: null,
    category: 'CITRUS',
    acquiredOn: null,
    plantedOn: null,
    estimatedAgeYears: null,
    status: 'ACTIVE',
    revision: 1,
    createdAt: '2026-11-01T00:00:00Z',
    updatedAt: '2026-11-01T00:00:00Z',
    photoCount: 0,
    tags: [],
    coverPhoto: null,
    currentContext: null,
    acquisition: null,
    ...over,
  };
}

function plantsEnvelope(items: unknown[], hasNext = false) {
  return {
    plants: {
      items,
      pageInfo: { endCursor: hasNext ? 'c1' : null, hasNextPage: hasNext },
    },
  };
}

/** fetch stub routing by operation name; records every call's variables. */
function stubFetch(handlers: Record<string, unknown>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as { query: string; variables?: object };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      calls.push({ op, vars: (body.variables ?? {}) as Record<string, unknown> });
      const h = handlers[op];
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

describe('CollectionPage', () => {
  let session: SessionService;
  let params$: BehaviorSubject<{ get(k: string): string | null }>;
  let current: Params;
  let navigate: ReturnType<typeof vi.fn>;

  const toMap = (p: Params) => ({ get: (k: string) => p[k] ?? null });
  const setUrl = (p: Params): void => {
    current = p;
    params$.next(toMap(current));
  };

  beforeEach(() => {
    current = {};
    params$ = new BehaviorSubject(toMap(current));
    navigate = vi.fn(async (_cmds: unknown, extras?: { queryParams?: Params }) => {
      const patch = extras?.queryParams ?? {};
      for (const [k, v] of Object.entries(patch)) {
        if (v === null) delete current[k];
        else current[k] = String(v);
      }
      params$.next(toMap(current));
      return true;
    });
    TestBed.configureTestingModule({
      providers: [
        { provide: Router, useValue: { navigate } },
        { provide: ActivatedRoute, useValue: { queryParamMap: params$ } },
      ],
    });
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({ workspaceId: 'w1', expiresAt: '2026-12-01T00:00:00Z', csrfToken: 'c' });
  });

  afterEach(() => vi.unstubAllGlobals());

  function render() {
    const fixture = TestBed.createComponent(CollectionPage);
    fixture.detectChanges();
    return fixture;
  }

  async function openFilters(fixture: ReturnType<typeof render>): Promise<HTMLElement> {
    const root = fixture.nativeElement as HTMLElement;
    [...root.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.includes('Find & filter'))!
      .click();
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    return document.querySelector<HTMLElement>('.dialog-panel')!;
  }
  function click(root: HTMLElement, label: string): void {
    [...root.querySelectorAll<HTMLButtonElement>('button')]
      .find((button) => button.textContent?.trim() === label)!
      .click();
  }
  it('renders real plant rows from the Plants operation', async () => {
    stubFetch({
      Plants: plantsEnvelope([plantRow(), plantRow({ id: 'p2', name: 'Fig “Brown Turkey”' })]),
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
    expect(text).toContain('Meyer lemon');
    expect(text).toContain('Brown Turkey');
    expect(text).toContain('2 shown');
  });

  it('unknown cultivar is shown as unknown, never guessed', async () => {
    stubFetch({ Plants: plantsEnvelope([plantRow({ cultivar: null })]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('Cultivar unknown');
  });

  it('URL params drive the fetch filter — archived views are deep-linkable', async () => {
    const calls = stubFetch({ Plants: plantsEnvelope([]) });
    setUrl({ status: 'archived', q: 'lemon', category: 'CITRUS', order: 'NAME_DESC' });
    render();
    await flush();
    const plants = calls.find((c) => c.op === 'Plants');
    expect(plants?.vars).toMatchObject({
      first: 20,
      filter: { query: 'lemon', category: 'CITRUS', status: ['ARCHIVED'] },
      order: 'NAME_DESC',
    });
  });

  it('default tab requests ACTIVE+DEAD — archived only when asked', async () => {
    const calls = stubFetch({ Plants: plantsEnvelope([]) });
    render();
    await flush();
    expect(calls.find((c) => c.op === 'Plants')?.vars).toMatchObject({
      filter: { status: ['ACTIVE', 'DEAD'] },
    });
  });

  it('applies all filters together and preserves the deep-linkable URL', async () => {
    const calls = stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    const panel = await openFilters(fixture);
    click(panel, 'Archived');
    fixture.detectChanges();
    await flush();
    expect(calls.filter((c) => c.op === 'Plants')).toHaveLength(1);
    expect(current['status']).toBeUndefined();
    panel
      .querySelector('form')!
      .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
    fixture.detectChanges();
    expect(current['status']).toBe('archived');
    expect(calls.filter((c) => c.op === 'Plants').at(-1)?.vars).toMatchObject({
      filter: { status: ['ARCHIVED'] },
    });
    expect(document.querySelector('.dialog-panel')).toBeNull();
    expect(
      (fixture.nativeElement as HTMLElement).querySelector('.filter-badge')?.textContent?.trim(),
    ).toBe('1');
  });
  it('keeps filter controls off the page and Cancel discards draft choices', async () => {
    stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).querySelector('input')).toBeNull();
    expect(document.querySelector('.dialog-panel')).toBeNull();
    let panel = await openFilters(fixture);
    click(panel, 'Citrus');
    click(panel, 'Cancel');
    await flush();
    fixture.detectChanges();
    expect(current).toEqual({});
    panel = await openFilters(fixture);
    expect(
      [...panel.querySelectorAll('button')]
        .find((b) => b.textContent?.trim() === 'All types')
        ?.getAttribute('aria-pressed'),
    ).toBe('true');
  });

  it('a failed load is an error state, never an empty garden', async () => {
    stubFetch({ Plants: 'network' });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('not an empty collection');
    expect(root.textContent).not.toContain('No plants yet');
  });

  it('a genuinely empty garden shows the add CTA', async () => {
    stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('No plants yet');
  });

  it('places one outlined Add Plant action after the real plant cards', async () => {
    stubFetch({ Plants: plantsEnvelope([plantRow(), plantRow({ id: 'p2', name: 'Alma fig' })]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.querySelectorAll('ui-plant-card')).toHaveLength(2);
    expect(root.querySelectorAll('.add-plant-card')).toHaveLength(1);
    expect(
      root.querySelector('.plant-grid')?.lastElementChild?.querySelector('.add-plant-card')
        ?.textContent,
    ).toContain('Add Plant');
    expect(root.querySelector('.page-head a')).toBeNull();
    expect(root.querySelector('.summary')?.textContent).toContain('2 shown');
  });

  it('keeps the creation action as the last row in list view', async () => {
    setUrl({ view: 'list' });
    stubFetch({ Plants: plantsEnvelope([plantRow()]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(
      root.querySelector('.rows')?.lastElementChild?.querySelector('.add-plant-card'),
    ).not.toBeNull();
    expect(root.querySelectorAll('.add-plant-card')).toHaveLength(1);
  });

  it('offers the same single creation tile for an empty loaded collection', async () => {
    stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.textContent).toContain('No plants yet');
    expect(root.querySelectorAll('.add-plant-card')).toHaveLength(1);
    expect(root.querySelectorAll('ui-plant-card')).toHaveLength(0);
  });

  it('distinguishes an empty search result from an empty collection', async () => {
    setUrl({ q: 'unknown tree', category: 'FIG' });
    stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.textContent).toContain('No matching plants');
    expect(root.textContent).not.toContain('No plants yet');
    expect(root.querySelectorAll('.add-plant-card')).toHaveLength(1);
  });

  it('does not show a creation tile over a failed or still-loading read', async () => {
    stubFetch({ Plants: 'network' });
    const failed = render();
    await flush();
    failed.detectChanges();
    expect((failed.nativeElement as HTMLElement).querySelector('.add-plant-card')).toBeNull();
    failed.destroy();
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    );
    const pending = render();
    pending.detectChanges();
    expect((pending.nativeElement as HTMLElement).querySelector('.add-plant-card')).toBeNull();
    expect(
      (pending.nativeElement as HTMLElement).querySelector('[aria-busy="true"]'),
    ).not.toBeNull();
    pending.destroy();
  });

  it('reset stays a draft until Apply clears search and all filter options', async () => {
    setUrl({ q: 'fig', status: 'archived', category: 'FIG', order: 'NAME_DESC', view: 'list' });
    stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    const panel = await openFilters(fixture);
    click(panel, 'Reset all');
    fixture.detectChanges();
    await flush();
    expect(current['q']).toBe('fig');
    expect(panel.querySelector<HTMLInputElement>('input[type=search]')?.value).toBe('');
    panel
      .querySelector('form')!
      .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await flush();
    expect(current).toEqual({});
  });
  it('does not expose archive actions on collection cards', async () => {
    const calls = stubFetch({ Plants: plantsEnvelope([plantRow()]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect(
      [...(fixture.nativeElement as HTMLElement).querySelectorAll('button')].some((b) =>
        /Archive|Restore/.test(b.textContent ?? ''),
      ),
    ).toBe(false);
    expect(calls.some((c) => c.op === 'ArchivePlant' || c.op === 'RestorePlant')).toBe(false);
  });

  it('navigation-away aborts in-flight work (destroy contract)', async () => {
    stubFetch({ Plants: plantsEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.destroy();
    // no assertion beyond “destroy does not throw or update signals later”
    await flush();
  });
});

describe('starter set', () => {
  it('is five trees and two groups, berry counts unknown', () => {
    expect(STARTER_PLANTS).toHaveLength(7);
    expect(STARTER_PLANTS.filter((p) => p.type === 'INDIVIDUAL')).toHaveLength(5);
    const groups = STARTER_PLANTS.filter((p) => p.type === 'GROUP');
    expect(groups).toHaveLength(2);
    for (const g of groups) expect(g.count).toBeNull();
  });

  it('idempotency keys are stable per plant (retried opt-in replays)', () => {
    expect(starterKey(STARTER_PLANTS[0])).toBe(starterKey(STARTER_PLANTS[0]));
    const keys = new Set(STARTER_PLANTS.map(starterKey));
    expect(keys.size).toBe(STARTER_PLANTS.length);
  });
});
