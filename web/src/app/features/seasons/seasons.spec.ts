import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { GardenKnowledgeDocument } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { GardenChatDialog } from '../assistant/garden-chat';
import { SeasonsPage } from './seasons';
import { GardenSettings } from './garden-settings';
const ok = (data: unknown) => ({ kind: 'ok', data, errors: [] });
const settings = {
  country: 'US',
  region: 'Texas',
  place: 'Test garden',
  latitude: null,
  longitude: null,
  timezone: 'America/Chicago',
  hardinessZone: null,
  zoneSystem: null,
  enabled: true,
  revision: 1,
};
const garden = { settings, plants: [], records: [] };
function deferred<T>() {
  let resolve!: (t: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}
describe('seasonal page and location actions', () => {
  let gql: { query: ReturnType<typeof vi.fn>; mutate: ReturnType<typeof vi.fn> };
  beforeEach(() => {
    gql = { query: vi.fn(async () => ok({ gardenKnowledge: garden })), mutate: vi.fn() };
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: GraphQLClient, useValue: gql }],
    });
  });
  afterEach(() => {
    TestBed.resetTestingModule();
    vi.restoreAllMocks();
  });
  async function render() {
    const f = TestBed.createComponent(SeasonsPage);
    f.detectChanges();
    for (let i = 0; i < 20; i++) await Promise.resolve();
    f.detectChanges();
    return f;
  }
  it('rejects stale previous-year reads', async () => {
    const f = await render(),
      c = f.componentInstance,
      old = deferred<unknown>();
    gql.query
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce(
        ok({ gardenKnowledge: { ...garden, settings: { ...settings, place: 'Current' } } }),
      );
    c.year.set(2025);
    const pending = c.load();
    c.year.set(2026);
    await c.load();
    old.resolve(ok({ gardenKnowledge: { ...garden, settings: { ...settings, place: 'Stale' } } }));
    await pending;
    expect(c.garden()?.settings.place).toBe('Current');
  });
  it('keeps failed queries honest instead of pretending the garden is empty', async () => {
    gql.query.mockResolvedValue({ kind: 'network' });
    const f = await render();
    expect(f.componentInstance.error()).toContain('reach');
    expect(f.componentInstance.garden()).toBeNull();
  });
  it('retries refresh with a stable intent, and uses a different intent for another plant', async () => {
    const f = await render(),
      c = f.componentInstance;
    gql.mutate.mockResolvedValue({ kind: 'network' });
    await c.refresh('a');
    await c.refresh('a');
    await c.refresh('b');
    expect(gql.mutate.mock.calls[0][1]).toEqual(gql.mutate.mock.calls[1][1]);
    expect(gql.mutate.mock.calls[2][1].clientMutationId).not.toBe(
      gql.mutate.mock.calls[0][1].clientMutationId,
    );
  });
  it('does not let a cancelled chat clear the busy state of its replacement', async () => {
    const f = TestBed.createComponent(GardenChatDialog),
      c = f.componentInstance;
    const old = deferred<unknown>(),
      current = deferred<unknown>();
    gql.mutate.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const p = c.ask('First question');
    c.cancelQuestion();
    const q = c.ask('Next question');
    old.resolve(ok({ askGarden: { answer: 'Old', facts: [], plantIds: [] } }));
    await p;
    expect(c.asking()).toBe(true);
    expect(c.answer()).toBeNull();
    current.resolve(ok({ askGarden: { answer: 'Current', facts: [], plantIds: [] } }));
    await q;
    expect(c.answer()?.answer).toBe('Current');
    expect(c.asking()).toBe(false);
  });
  it('location is only searched on explicit action and manual edits clear stale coordinates', async () => {
    const f = TestBed.createComponent(GardenSettings);
    f.componentRef.setInput('settings', settings);
    f.detectChanges();
    expect(gql.query).not.toHaveBeenCalled();
    const c = f.componentInstance;
    await c.edit();
    expect(c.open()).toBe(true);
    expect(gql.query).not.toHaveBeenCalled();
    c.select({
      name: 'Paris',
      country: 'FR',
      region: 'Île-de-France',
      latitude: '48.8566',
      longitude: '2.3522',
      timezone: 'Europe/Paris',
    });
    expect(c.latitude()).toBe('48.857');
    c.geography('place', 'Different garden');
    expect(c.latitude()).toBe('');
    expect(c.longitude()).toBe('');
  });
  it('preserves location draft and request identity after uncertain save', async () => {
    const f = TestBed.createComponent(GardenSettings);
    f.componentRef.setInput('settings', settings);
    f.detectChanges();
    const c = f.componentInstance;
    await c.edit();
    gql.mutate.mockResolvedValue({ kind: 'network' });
    await c.save();
    await c.save();
    expect(c.open()).toBe(true);
    expect(c.place()).toBe('Test garden');
    expect(gql.mutate.mock.calls[1][1]).toEqual(gql.mutate.mock.calls[0][1]);
  });
});
