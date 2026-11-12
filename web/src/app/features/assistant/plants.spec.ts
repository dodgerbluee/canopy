import { describe, it, expect, vi } from 'vitest';
import { GraphQLClient } from '../../core/graphql/client';
import { loadGardenPlants } from './plants';
describe('complete plant selection', () => {
  it('loads every page and explicitly includes archived history when requested', async () => {
    const query = vi
      .fn()
      .mockResolvedValueOnce({
        kind: 'ok', errors: [],
        data: {
          plants: {
            items: [{ id: 'one', name: 'Current' }],
            pageInfo: { hasNextPage: true, endCursor: 'next' },
          },
        },
      })
      .mockResolvedValueOnce({
        kind: 'ok', errors: [],
        data: {
          plants: {
            items: [{ id: 'old', name: 'Archived' }],
            pageInfo: { hasNextPage: false, endCursor: null },
          },
        },
      });
    const signal = new AbortController().signal;
    const plants = await loadGardenPlants({ query } as unknown as GraphQLClient, signal, true);
    expect(plants.map((p) => p.id)).toEqual(['one', 'old']);
    expect(query.mock.calls[1][1].after).toBe('next');
    expect(query.mock.calls[0][1].filter.status).toContain('ARCHIVED');
    expect(query.mock.calls[1][2]).toBe(signal);
  });
  it('refuses a repeating cursor instead of looping or presenting a partial list', async () => {
    const query = vi
      .fn()
      .mockResolvedValue({
        kind: 'ok', errors: [],
        data: { plants: { items: [], pageInfo: { hasNextPage: true, endCursor: 'same' } } },
      });
    await expect(
      loadGardenPlants({ query } as unknown as GraphQLClient, new AbortController().signal),
    ).rejects.toThrow('fully loaded');
    expect(query).toHaveBeenCalledTimes(2);
  });
});
