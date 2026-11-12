import { describe, it, expect, vi } from 'vitest';
import { GraphQLClient } from '../../core/graphql/client';
import {
  calendarEvents,
  calendarPlants,
  monthDays,
  shiftMonth,
  rainMillimeters,
  workspaceDay,
} from './calendar-data';
const page = (items: unknown[], hasNextPage = false, endCursor: string | null = null) => ({
  items,
  pageInfo: { hasNextPage, endCursor },
});
const ok = (data: unknown) => ({ kind: 'ok', data, errors: [] });
const client = (query: ReturnType<typeof vi.fn>) => ({ query }) as unknown as GraphQLClient;
const signal = new AbortController().signal;
describe('calendar dates and depth', () => {
  it('builds complete Sunday-Saturday grids across leap years and year rollover', () => {
    const days = monthDays('2026-11');
    expect(days).toHaveLength(35);
    expect(days[0]).toBe('2026-11-01');
    expect(days.at(-1)).toBe('2026-12-05');
    expect(monthDays('2024-02')).toContain('2024-02-29');
    expect(monthDays('2025-02')).not.toContain('2025-02-29');
    expect(shiftMonth('2026-12', 1)).toBe('2027-01');
    expect(shiftMonth('2026-01', -1)).toBe('2025-12');
  });
  it('uses workspace day across midnight and DST independent of browser zone', () => {
    expect(workspaceDay(new Date('2026-11-02T05:30Z'), 'America/Chicago')).toBe('2026-11-01');
    expect(workspaceDay(new Date('2026-11-02T06:30Z'), 'America/Chicago')).toBe('2026-11-02');
    expect(workspaceDay(new Date('2026-11-02T05:30Z'), 'Asia/Tokyo')).toBe('2026-11-02');
  });
  it('converts inch depth exactly while preserving unknown and zero', () => {
    expect(rainMillimeters('', 'in')).toBeNull();
    expect(rainMillimeters('0', 'in')).toBe('0');
    expect(rainMillimeters('0.1', 'in')).toBe('2.54');
    expect(rainMillimeters('1.25', 'in')).toBe('31.75');
    expect(rainMillimeters('0.0001', 'in')).toBe('0.00254');
    expect(rainMillimeters('100', 'mm')).toBe('100');
    expect(rainMillimeters('0.123456', 'mm')).toBe('0.123456');
    for (const value of ['-1', '1e3', 'NaN', '1.2.3', '10001', '0.0000001'])
      expect(() => rainMillimeters(value, 'mm')).toThrow();
  });
});
describe('calendar complete reads', () => {
  it('reads every event page without changing server capture days', async () => {
    const a = { id: 'photo-captured-earlier', day: '2026-11-04', kind: 'PHOTO' };
    const query = vi
      .fn()
      .mockResolvedValueOnce(ok({ calendarEvents: page([a], true, 'next') }))
      .mockResolvedValueOnce(
        ok({ calendarEvents: page([{ id: 'rain', day: '2026-11-05', kind: 'RAIN' }]) }),
      );
    const items = await calendarEvents(client(query), '2026-11-01', '2026-12-05', signal);
    expect(items).toHaveLength(2);
    expect(items[0]).toEqual(a);
    expect(query.mock.calls[1][1]).toEqual({
      from: '2026-11-01',
      to: '2026-12-05',
      first: 100,
      after: 'next',
    });
  });
  it('includes archived plants in historical filters and reads all pages', async () => {
    const query = vi
      .fn()
      .mockResolvedValueOnce(ok({ plants: page([{ id: 'a', status: 'ARCHIVED' }], true, 'next') }))
      .mockResolvedValueOnce(ok({ plants: page([{ id: 'b', status: 'ACTIVE' }]) }));
    expect(await calendarPlants(client(query), signal)).toHaveLength(2);
    expect(query.mock.calls[0][1].filter).toBeUndefined();
  });
  it('never presents failed or partial reads as an empty month', async () => {
    for (const response of [
      { kind: 'network' },
      { kind: 'ok', data: null, errors: [{ message: 'Failed month' }] },
    ]) {
      const query = vi.fn().mockResolvedValue(response);
      await expect(
        calendarEvents(client(query), '2026-11-01', '2026-11-30', signal),
      ).rejects.toThrow();
    }
    const query = vi.fn().mockResolvedValue(ok({ calendarEvents: page([], true, 'loop') }));
    await expect(calendarEvents(client(query), '2026-11-01', '2026-11-30', signal)).rejects.toThrow(
      'incomplete',
    );
  });
});
