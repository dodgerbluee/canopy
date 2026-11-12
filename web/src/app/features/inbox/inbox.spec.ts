import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import { InboxPage } from './inbox';

/** Inbox rows exactly as the frozen NotificationFields fragment ships. */
function notification(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 'n-1',
    channel: 'INAPP',
    dedupKey: 'occ:oc-1:INAPP',
    refKind: 'OCCURRENCE',
    refId: 'oc-1',
    title: 'Water Mission fig',
    body: 'Deep soak is due today.',
    digestDate: null,
    createdAt: '2026-11-02T14:00:00Z',
    readAt: null,
    ...over,
  };
}

function inboxEnvelope(items: unknown[], hasNext = false, endCursor: string | null = null) {
  return { notifications: { items, pageInfo: { endCursor, hasNextPage: hasNext } } };
}

function stubFetch(handlers: Record<string, unknown>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as { query: string; variables?: object };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      const vars = (body.variables ?? {}) as Record<string, unknown>;
      calls.push({ op, vars });
      let h = handlers[op];
      if (typeof h === 'function') h = (h as (v: Record<string, unknown>) => unknown)(vars);
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

describe('InboxPage', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({ workspaceId: 'w1', expiresAt: '2026-12-01T00:00:00Z', csrfToken: 'c' });
  });

  afterEach(() => vi.unstubAllGlobals());

  function render(handlers: Record<string, unknown>) {
    stubFetch(handlers);
    const fixture = TestBed.createComponent(InboxPage);
    fixture.detectChanges();
    return fixture;
  }

  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    fixture.nativeElement.textContent ?? '';
  const buttons = (root: HTMLElement, label: string) =>
    [...root.querySelectorAll('button')].filter((b) => b.textContent?.trim() === label);

  it('lists unread items with a mark-read affordance and read items without one', async () => {
    const fixture = render({
      Notifications: inboxEnvelope([
        notification(),
        notification({
          id: 'n-2',
          channel: 'EMAIL',
          digestDate: '2026-11-02',
          title: 'Care digest 2026-11-02: 1 task(s)',
        }),
        notification({ id: 'n-3', readAt: '2026-11-01T10:00:00Z' }),
      ]),
    });
    await flush();
    fixture.detectChanges();

    const text = textOf(fixture);
    expect(text).toContain('Water Mission fig');
    expect(text).toContain('Care digest 2026-11-02: 1 task(s)');
    expect(text).toContain('Email digest');
    expect(text).toContain('2 unread');
    const el = fixture.nativeElement as HTMLElement;
    expect(buttons(el, 'Mark as read')).toHaveLength(2);
    expect(el.querySelectorAll('li.is-unread')).toHaveLength(2);
  });

  it('marks read through the mutation, then reloads the truth', async () => {
    let rows = [notification()];
    const calls = stubFetch({
      Notifications: () => inboxEnvelope(rows),
      MarkNotificationRead: (vars: Record<string, unknown>) => {
        const input = vars['input'] as { notificationId: string };
        expect(input.notificationId).toBe('n-1');
        rows = [notification({ readAt: '2026-11-02T15:00:00Z' })];
        return { markNotificationRead: { notification: rows[0], problems: [] } };
      },
    });
    const fixture = TestBed.createComponent(InboxPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    buttons(el, 'Mark as read')[0].click();
    await flush();
    await flush();
    fixture.detectChanges();

    const read = calls.filter((c) => c.op === 'MarkNotificationRead');
    expect(read).toHaveLength(1);
    expect(read[0].vars['clientMutationId']).toMatch(/:mark-notification-read$/);
    expect(textOf(fixture)).not.toContain('Mark as read');
    expect(textOf(fixture)).not.toContain('unread');
    // the list is re-read after the mutation, never patched locally
    expect(calls.filter((c) => c.op === 'Notifications')).toHaveLength(2);
  });

  it('a payload problem keeps the item unread and says so', async () => {
    stubFetch({
      Notifications: inboxEnvelope([notification()]),
      MarkNotificationRead: {
        markNotificationRead: {
          notification: null,
          problems: [
            {
              code: 'NOT_FOUND',
              field: 'input.notificationId',
              message: 'Notification not found.',
            },
          ],
        },
      },
    });
    const fixture = TestBed.createComponent(InboxPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    buttons(el, 'Mark as read')[0].click();
    await flush();
    await flush();
    fixture.detectChanges();

    expect(buttons(el, 'Mark as read')).toHaveLength(1);
    const toasts = TestBed.inject(UiToasts);
    expect(
      toasts
        .toasts()
        .some((t) => t.kind === 'error' && t.message.includes('Notification not found.')),
    ).toBe(true);
  });

  it('the unread filter re-queries with unreadOnly and back', async () => {
    const calls = stubFetch({ Notifications: inboxEnvelope([notification()]) });
    const fixture = TestBed.createComponent(InboxPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    const toggle = [...el.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Showing all'),
    )!;
    toggle.click();
    await flush();
    fixture.detectChanges();

    const queries = calls.filter((c) => c.op === 'Notifications');
    expect(queries[0].vars['unreadOnly']).toBe(false);
    expect(queries[1].vars['unreadOnly']).toBe(true);
    expect(textOf(fixture)).toContain('Showing unread');
  });

  it('a failed load is an error state, never an empty inbox', async () => {
    stubFetch({ Notifications: 'network' });
    const fixture = render({ Notifications: 'network' });
    await flush();
    fixture.detectChanges();

    expect(textOf(fixture)).toContain('Inbox failed to load');
    expect(textOf(fixture)).toContain('not an empty inbox');
  });
});
