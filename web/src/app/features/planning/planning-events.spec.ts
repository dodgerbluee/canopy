import { describe, expect, it, vi } from 'vitest';
import { planningEvents } from './planning-events';
import type { GraphQLClient } from '../../core/graphql/client';
const page = (items: unknown[], cursor: string | null) => ({
  kind: 'ok',
  errors: [],
  data: { plantingEvents: { items, pageInfo: { hasNextPage: !!cursor, endCursor: cursor } } },
});
describe('separate planning projection reads', () => {
  it('consumes every page without making owned plants or changing civil days', async () => {
    const query = vi
      .fn()
      .mockResolvedValueOnce(page([{ id: 'a', planId: 'future', day: '2027-03-14' }], 'next'))
      .mockResolvedValueOnce(page([{ id: 'b', planId: 'future', day: '2027-03-15' }], null));
    const signal = new AbortController().signal;
    const rows = await planningEvents(
      { query } as unknown as GraphQLClient,
      '2027-03-01',
      '2027-03-31',
      signal,
    );
    expect(rows.map((r) => r.day)).toEqual(['2027-03-14', '2027-03-15']);
    expect(query.mock.calls[1][1]).toEqual({
      from: '2027-03-01',
      to: '2027-03-31',
      first: 100,
      after: 'next',
    });
    expect(query.mock.calls[1][2]).toBe(signal);
  });
  it('refuses incomplete repeated cursors instead of calling the range empty', async () => {
    const query = vi.fn().mockResolvedValue(page([], 'repeat'));
    await expect(
      planningEvents(
        { query } as unknown as GraphQLClient,
        '2027-01-01',
        '2027-01-31',
        new AbortController().signal,
      ),
    ).rejects.toThrow('completely');
    expect(query).toHaveBeenCalledTimes(2);
  });
  it('fails honestly on an unavailable planning service', async () => {
    const query = vi.fn().mockResolvedValue({ kind: 'network' });
    await expect(
      planningEvents(
        { query } as unknown as GraphQLClient,
        '2027-01-01',
        '2027-01-31',
        new AbortController().signal,
      ),
    ).rejects.toThrow('reach');
  });
});
