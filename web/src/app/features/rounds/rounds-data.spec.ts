import { describe, expect, it, vi } from 'vitest';
import { PlantsDocument, JournalDocument } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { localDateInput } from '../journal/journal-view';
import { loadRoundHistory, loadRoundPlants, roundDay } from './rounds-data';
const date = new Date('2026-11-05T02:15:00.000Z');
const connection = (items: unknown[], hasNextPage = false, endCursor: string | null = null) => ({
  items,
  pageInfo: { hasNextPage, endCursor },
});
const ok = (data: unknown) => ({ kind: 'ok', data, errors: [] });
const plant = (id: string, status = 'ACTIVE') => ({ id, status, name: id });
const activity = (id: string, plantId: string, extra: Record<string, unknown> = {}) => ({
  __typename: 'ActivityEntry',
  id: 'entry-' + id,
  plantId,
  occurredAt: date.toISOString(),
  tombstonedAt: null,
  activity: {
    id,
    plantId,
    kind: 'WATER',
    occurredAt: date.toISOString(),
    voidedAt: null,
    ...extra,
  },
});
const photo = (id: string, plantId: string, extra: Record<string, unknown> = {}) => ({
  __typename: 'PhotoEntry',
  id: 'entry-' + id,
  plantId,
  occurredAt: date.toISOString(),
  tombstonedAt: null,
  photo: {
    id,
    plantId,
    takenAt: date.toISOString(),
    dateSource: 'USER',
    deletedAt: null,
    ...extra,
  },
});
function client(query: ReturnType<typeof vi.fn>) {
  return { query } as unknown as GraphQLClient;
}

describe('rounds data', () => {
  it('uses local calendar boundaries including the full last millisecond', () => {
    const d = roundDay(date);
    expect(d.key).toBe(localDateInput(date.toISOString()));
    const start = new Date(d.from),
      end = new Date(d.to);
    expect(start.getHours()).toBe(0);
    expect(start.getMinutes()).toBe(0);
    expect(end.getHours()).toBe(23);
    expect(end.getMilliseconds()).toBe(999);
    expect(localDateInput(d.from)).toBe(d.key);
    expect(localDateInput(d.to)).toBe(d.key);
  });
  it('handles daylight saving transitions without assuming every day is24hours', () => {
    const spring = new Date(2026, 2, 8, 12),
      fall = new Date(2026, 10, 1, 12);
    for (const day of [spring, fall]) {
      const range = roundDay(day);
      const start = new Date(day.getFullYear(), day.getMonth(), day.getDate());
      const next = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 1);
      expect(Date.parse(range.to) - Date.parse(range.from) + 1).toBe(
        next.getTime() - start.getTime(),
      );
    }
  });
  it('rejects invalid dates rather than emitting a broken query range', () => {
    expect(() => roundDay(new Date('bad'))).toThrow();
  });
  it('paginates all active plants with an explicit status/order and suppresses duplicate IDs', async () => {
    const query = vi
      .fn()
      .mockResolvedValueOnce(ok({ plants: connection([plant('fig')], true, 'cursor-1') }))
      .mockResolvedValueOnce(
        ok({ plants: connection([plant('fig'), plant('lime'), plant('archived', 'ARCHIVED')]) }),
      );
    const signal = new AbortController().signal;
    const rows = await loadRoundPlants(client(query), signal);
    expect(rows.map((p) => p.id)).toEqual(['fig', 'lime']);
    expect(query.mock.calls[0][0]).toBe(PlantsDocument);
    expect(query.mock.calls[0][1]).toMatchObject({
      first: 100,
      filter: { status: ['ACTIVE'] },
      order: 'NAME_ASC',
    });
    expect(query.mock.calls[1][1].after).toBe('cursor-1');
    expect(query.mock.calls[0][2]).toBe(signal);
  });
  it('does not treat a failed garden query as an empty garden', async () => {
    for (const result of [
      { kind: 'network' },
      { kind: 'auth' },
      ok(null),
      { kind: 'ok', data: null, errors: [{ message: 'server failed' }] },
    ]) {
      await expect(loadRoundPlants(client(vi.fn().mockResolvedValue(result)))).rejects.toThrow();
    }
  });
  it('refuses a non-advancing cursor instead of silently omitting later plants', async () => {
    const query = vi
      .fn()
      .mockResolvedValue(ok({ plants: connection([plant('fig')], true, 'stuck') }));
    await expect(loadRoundPlants(client(query))).rejects.toThrow();
    expect(query.mock.calls.length).toBeLessThan(4);
  });
  it('counts only actual non-voided watering and dated photos today across every journal page', async () => {
    const tombstoned = { ...activity('old', 'fig'), tombstonedAt: date.toISOString() };
    const yesterday = new Date(date);
    yesterday.setDate(yesterday.getDate() - 1);
    const page1 = [
      activity('water', 'fig'),
      activity('note', 'lime', { kind: 'NOTE' }),
      activity('void', 'lime', { voidedAt: date.toISOString() }),
      photo('photo', 'fig'),
      photo('unknown', 'lime', { takenAt: null, dateSource: 'UPLOAD_FALLBACK' }),
      tombstoned,
    ];
    const page2 = [
      photo('photo', 'fig'),
      photo('second', 'fig', { dateSource: 'EXIF' }),
      photo('deleted', 'lime', { deletedAt: date.toISOString() }),
      photo('yesterday', 'lime', { takenAt: yesterday.toISOString() }),
      activity('older', 'lime', { occurredAt: yesterday.toISOString() }),
    ];
    const query = vi
      .fn()
      .mockResolvedValueOnce(ok({ journal: connection(page1, true, 'next') }))
      .mockResolvedValueOnce(ok({ journal: connection(page2) }));
    const history = await loadRoundHistory(client(query), date);
    expect([...history.watered]).toEqual(['fig']);
    expect([...history.photos.get('fig')!]).toEqual(['photo', 'second']);
    expect(history.photos.has('lime')).toBe(false);
    expect(history.dayKey).toBe(localDateInput(date.toISOString()));
    expect(query.mock.calls[0][0]).toBe(JournalDocument);
    expect(query.mock.calls[0][1].filter).toMatchObject({
      entryTypes: ['ACTIVITY', 'PHOTO'],
      includeTombstoned: false,
      ...{ from: roundDay(date).from, to: roundDay(date).to },
    });
    expect(query.mock.calls[1][1].after).toBe('next');
  });
  it('surfaces a failed later history page rather than claiming a partially checked day', async () => {
    const query = vi
      .fn()
      .mockResolvedValueOnce(ok({ journal: connection([activity('water', 'fig')], true, 'next') }))
      .mockResolvedValueOnce({ kind: 'network' });
    await expect(loadRoundHistory(client(query), date)).rejects.toThrow();
  });
});
