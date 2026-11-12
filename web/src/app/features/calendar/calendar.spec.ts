import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PlantsDocument, SessionDocument } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import { CalendarPage } from './calendar';
const ok = (data: unknown) => ({ kind: 'ok', data, errors: [] });
const connection = (items: unknown[]) => ({
  items,
  pageInfo: { hasNextPage: false, endCursor: null },
});
const water = {
  id: 'water',
  entityId: 'water',
  plantId: 'lemon',
  day: '2026-11-04',
  kind: 'ACTIVITY',
  activityKind: 'WATER',
  title: 'Watered',
  amountMm: null,
  note: null,
  revision: null,
};
const rain = {
  id: 'rain:existing',
  entityId: 'existing',
  plantId: null,
  day: '2026-11-04',
  kind: 'RAIN',
  activityKind: null,
  title: 'Rain recorded',
  amountMm: null,
  note: null,
  revision: 1,
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}
describe('calendar state', () => {
  let gql: { query: ReturnType<typeof vi.fn>; mutate: ReturnType<typeof vi.fn> };
  let session: { info: ReturnType<typeof signal<{ workspaceId: string }>> };
  beforeEach(() => {
    session = { info: signal({ workspaceId: 'fixture' }) };
    gql = {
      query: vi.fn(async (doc: unknown) =>
        ok(
          doc === SessionDocument
            ? {
                session: {
                  workspace: { timezone: 'America/Chicago', displayName: 'Garden', location: null },
                },
              }
            : doc === PlantsDocument
              ? {
                  plants: connection([
                    { id: 'lemon', name: 'Lemon', status: 'ACTIVE', coverPhoto: null },
                    { id: 'lime', name: 'Lime', status: 'ACTIVE', coverPhoto: null },
                  ]),
                }
              : { calendarEvents: connection([water, rain]) },
        ),
      ),
      mutate: vi.fn(),
    };
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: GraphQLClient, useValue: gql },
        { provide: SessionService, useValue: session },
        { provide: UiToasts, useValue: { ok: vi.fn(), error: vi.fn() } },
      ],
    });
  });
  afterEach(() => {
    TestBed.resetTestingModule();
    vi.restoreAllMocks();
  });
  async function render() {
    const fixture = TestBed.createComponent(CalendarPage);
    fixture.detectChanges();
    for (let i = 0; i < 30; i++) await Promise.resolve();
    fixture.detectChanges();
    return fixture;
  }
  it('plant visibility filters never hide independent area rain or mutate records', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.selectedDay.set('2026-11-04');
    expect(c.dayEvents()).toHaveLength(2);
    c.togglePlant('lemon');
    expect(c.dayEvents().map((e) => e.id)).toEqual(['rain:existing']);
    c.showRain.set(false);
    expect(c.dayEvents()).toHaveLength(0);
    c.chooseAll(true);
    expect(c.dayEvents().map((e) => e.id)).toEqual(['water']);
    expect(gql.mutate).not.toHaveBeenCalled();
  });
  it('double taps share one in-flight save; uncertain replies retry the identical intent', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.selectedDay.set('2026-11-04');
    c.addRain();
    c.rainAmount.set('0.1');
    const first = deferred<unknown>();
    gql.mutate
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(
        ok({
          recordRain: {
            rain: { id: 'new', day: '2026-11-04', amountMm: '2.54', note: null, revision: 1 },
            problems: [],
          },
        }),
      );
    const saving = c.saveRain();
    await c.saveRain();
    expect(gql.mutate).toHaveBeenCalledTimes(1);
    first.resolve({ kind: 'network' });
    await saving;
    expect(c.rainOpen()).toBe(true);
    expect(c.saveError()).toContain('draft is still here');
    expect(c.rainAmount()).toBe('0.1');
    await c.saveRain();
    expect(gql.mutate).toHaveBeenCalledTimes(2);
    expect(gql.mutate.mock.calls[1][1]).toEqual(gql.mutate.mock.calls[0][1]);
    expect(gql.mutate.mock.calls[0][1].input.amountMm).toBe('2.54');
    expect(c.rainOpen()).toBe(false);
  });
  it('late previous-month responses cannot overwrite the current month', async () => {
    const f = await render(),
      c = f.componentInstance;
    const first = deferred<unknown>();
    gql.query
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce(ok({ calendarEvents: connection([{ ...water, id: 'current' }]) }));
    c.month.set('2026-12');
    const old = c.loadMonth();
    c.month.set('2027-01');
    await c.loadMonth();
    first.resolve(ok({ calendarEvents: connection([{ ...water, id: 'stale' }]) }));
    await old;
    expect(c.events().map((e) => e.id)).toEqual(['current']);
    expect(c.loading()).toBe(false);
  });
  it('unknown amount stays null, no optimistic success on a failed save', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.selectedDay.set('2026-11-04');
    c.addRain();
    gql.mutate.mockResolvedValue(
      ok({ recordRain: { rain: null, problems: [{ message: 'Rain unavailable' }] } }),
    );
    await c.saveRain();
    expect(gql.mutate.mock.calls[0][1].input.amountMm).toBeNull();
    expect(c.saveError()).toContain('Rain unavailable');
    expect(c.rainOpen()).toBe(true);
  });
  it('a save finishing after workspace change cannot publish into the new workspace', async () => {
    const f = await render(),
      c = f.componentInstance;
    c.selectedDay.set('2026-11-04');
    c.addRain();
    const response = deferred<unknown>();
    gql.mutate.mockReturnValueOnce(response.promise);
    const saving = c.saveRain();
    session.info.set({ workspaceId: 'other' });
    response.resolve(
      ok({
        recordRain: {
          rain: { id: 'foreign', day: '2026-11-04', amountMm: null, note: null, revision: 1 },
          problems: [],
        },
      }),
    );
    await saving;
    expect(c.events().some((e) => e.entityId === 'foreign')).toBe(false);
  });
});
