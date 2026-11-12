import { signal } from '@angular/core';
import type { GqlResult } from './client';

/** One page of a connection, per the SDL convention: items + pageInfo. */
export interface Page<T> {
  items: T[];
  endCursor: string | null;
  hasNextPage: boolean;
}

export type PageFetcher<T> = (after: string | null) => Promise<GqlResult<Page<T>>>;

/**
 * Cursor pagination store (T08 convention). destroy() is the contract for
 * navigation-away: every in-flight resolution — resolved, errored or
 * superseded — is dropped, so an aborted navigation can never update a
 * destroyed store.
 */
export class PageStore<T> {
  readonly items = signal<T[]>([]);
  readonly loading = signal(false);
  readonly error = signal<string | null>(null);

  private endCursor: string | null = null;
  private hasNext = false;
  private loaded = false;
  private destroyed = false;
  private seq = 0;

  constructor(private readonly fetchPage: PageFetcher<T>) {}

  /** True while another page may be requested. */
  get canLoadMore(): boolean {
    return !this.destroyed && !this.loading() && (!this.loaded || this.hasNext);
  }

  async loadMore(): Promise<void> {
    if (!this.canLoadMore) return;
    const call = ++this.seq;
    this.loading.set(true);
    const res = await this.fetchPage(this.endCursor);
    if (this.destroyed || call !== this.seq) return; // dropped: dead or superseded
    this.loading.set(false);
    if (res.kind === 'aborted') return; // caller navigated away; keep last good state
    if (res.kind !== 'ok') {
      this.error.set(res.kind === 'auth' ? 'Session expired — sign in again.' : 'Could not load. Retry.');
      return;
    }
    if (res.data === null) {
      this.error.set(res.errors[0]?.message ?? 'Could not load. Retry.');
      return;
    }
    const page = res.data;
    this.items.update((cur) => cur.concat(page.items));
    this.endCursor = page.endCursor;
    this.hasNext = page.hasNextPage;
    this.loaded = true;
    this.error.set(null);
  }

  /** Restart from page one, dropping what is on screen. */
  async refresh(): Promise<void> {
    this.items.set([]);
    this.endCursor = null;
    this.hasNext = false;
    this.loaded = false;
    await this.loadMore();
  }

  destroy(): void {
    this.destroyed = true;
    this.seq++;
  }
}
