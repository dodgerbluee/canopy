import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import type { GqlResult } from './client';
import { PageStore, type Page } from './page-store';

/** Page-store specs are driven by the frozen plants-page contract fixture. */
const fixture = JSON.parse(
  readFileSync(resolve('..', 'contracts/fixtures/api/plants-page.json'), 'utf8'),
) as { data: { plants: { items: Array<{ id: string; name: string }>; pageInfo: { endCursor: string | null; hasNextPage: boolean } } } };

interface PlantRow {
  id: string;
  name: string;
}

function fixturePage(): Page<PlantRow> {
  const conn = fixture.data.plants;
  return {
    items: conn.items.map((p) => ({ id: p.id, name: p.name })),
    endCursor: conn.pageInfo.endCursor,
    hasNextPage: conn.pageInfo.hasNextPage,
  };
}

function ok<T>(page: Page<T>): GqlResult<Page<T>> {
  return { kind: 'ok', data: page, errors: [] };
}

describe('PageStore', () => {
  it('loads the fixture page and exposes its items and cursor', async () => {
    const seen: Array<string | null> = [];
    const store = new PageStore<PlantRow>(async (after) => {
      seen.push(after);
      return ok(fixturePage());
    });
    await store.loadMore();
    expect(seen).toEqual([null]);
    expect(store.items().map((p) => p.name)).toEqual(['Meyer lemon', 'Blackberry group']);
    expect(store.canLoadMore).toBe(false); // fixture says hasNextPage: false
  });

  it('threads the endCursor into the next request and stops when exhausted', async () => {
    const seen: Array<string | null> = [];
    const store = new PageStore<PlantRow>(async (after) => {
      seen.push(after);
      if (seen.length === 1) {
        return ok({ items: [{ id: 'a', name: 'A' }], endCursor: 'cur-1', hasNextPage: true });
      }
      return ok({ items: [{ id: 'b', name: 'B' }], endCursor: 'cur-2', hasNextPage: false });
    });
    await store.loadMore();
    await store.loadMore();
    expect(seen).toEqual([null, 'cur-1']);
    expect(store.items().map((p) => p.id)).toEqual(['a', 'b']);
    expect(store.canLoadMore).toBe(false);
  });

  it('a destroyed store never updates, even when the in-flight page resolves', async () => {
    let resolvePage!: (r: GqlResult<Page<PlantRow>>) => void;
    const pending = new Promise<GqlResult<Page<PlantRow>>>((res) => (resolvePage = res));
    const store = new PageStore<PlantRow>(() => pending);

    const load = store.loadMore();
    store.destroy();
    resolvePage(ok(fixturePage()));
    await load;

    expect(store.items()).toEqual([]);
    expect(store.error()).toBeNull();
    expect(store.canLoadMore).toBe(false);
  });

  it('an aborted navigation leaves the last good state untouched', async () => {
    const store = new PageStore<PlantRow>(async () => ok(fixturePage()));
    await store.loadMore();
    const before = store.items();

    const store2 = new PageStore<PlantRow>(async () => ({ kind: 'aborted' }));
    await store2.loadMore();
    expect(store2.items()).toEqual([]);
    expect(store2.error()).toBeNull();

    // original store is unaffected by any of this
    expect(store.items()).toBe(before);
  });

  it('auth failure surfaces an honest retry message', async () => {
    const store = new PageStore<PlantRow>(async () => ({ kind: 'auth' }));
    await store.loadMore();
    expect(store.error()).toMatch(/sign in/i);
    expect(store.loading()).toBe(false);
  });

  it('data:null with typed errors shows the first error message', async () => {
    const store = new PageStore<PlantRow>(async () => ({
      kind: 'ok',
      data: null,
      errors: [{ message: 'Invalid cursor.', extensions: { code: 'VALIDATION' } }],
    }));
    await store.loadMore();
    expect(store.error()).toBe('Invalid cursor.');
  });

  it('refresh restarts pagination from the first page', async () => {
    const seen: Array<string | null> = [];
    const store = new PageStore<PlantRow>(async (after) => {
      seen.push(after);
      return ok({ items: [{ id: seen.length === 1 ? 'a' : 'b', name: 'x' }], endCursor: 'c1', hasNextPage: false });
    });
    await store.loadMore();
    await store.refresh();
    expect(seen).toEqual([null, null]);
    expect(store.items().map((p) => p.id)).toEqual(['b']);
  });

  it('concurrent loadMore calls are collapsed to one in-flight request', async () => {
    const fetchPage = vi
      .fn<(after: string | null) => Promise<GqlResult<Page<PlantRow>>>>()
      .mockResolvedValue(ok({ items: [{ id: 'a', name: 'A' }], endCursor: 'c1', hasNextPage: true }));
    const store = new PageStore<PlantRow>(fetchPage);
    await Promise.all([store.loadMore(), store.loadMore()]);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });
});
