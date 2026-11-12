import { requestId } from '../../core/request-id';
import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  inject,
  signal,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import { DatePipe } from '@angular/common';
import {
  MarkNotificationReadDocument,
  NotificationsDocument,
  type NotificationsQuery,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { PageStore, type Page } from '../../core/graphql/page-store';
import { UiIcon } from '../../ui/icon';
import { UiSkeleton } from '../../ui/skeleton';
import { UiToasts } from '../../ui/toast';
import type { PayloadProblem } from '../collection/shared';

type InboxItem = NotificationsQuery['notifications']['items'][number];

/**
 * Notification inbox (T27): in-app intents and daily-digest records in
 * one list over the frozen keyset connection. Read state is the only
 * mutation offered and it is explicit — opening the inbox never marks
 * anything read. Unread items are visually distinct and countable.
 */
@Component({
  selector: 'inbox-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, DatePipe, UiIcon, UiSkeleton],
  templateUrl: './inbox.html',
  styleUrl: './inbox.css',
})
export class InboxPage implements OnInit, OnDestroy {
  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  private readonly abort = new AbortController();

  protected readonly store: PageStore<InboxItem>;
  protected readonly unreadOnly = signal(false);
  protected readonly busy = signal(false);
  protected readonly hasNext = signal(false);

  constructor() {
    this.store = new PageStore((after) => this.fetchPage(after));
  }

  ngOnInit(): void {
    void this.store.refresh();
  }

  ngOnDestroy(): void {
    this.abort.abort();
    this.store.destroy();
  }

  protected readonly unreadShown = computed(
    () => this.store.items().filter((n) => !n.readAt).length,
  );

  protected channelLabel(n: InboxItem): string {
    if (n.channel === 'EMAIL') return n.digestDate ? 'Email digest' : 'Email';
    return 'In-app';
  }

  protected toggleFilter(): void {
    this.unreadOnly.set(!this.unreadOnly());
    void this.store.refresh();
  }

  protected retry(): void {
    void this.store.refresh();
  }

  protected async markRead(n: InboxItem): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(
        MarkNotificationReadDocument,
        {
          input: { notificationId: n.id },
          clientMutationId: `${requestId()}:mark-notification-read`,
        },
        this.abort.signal,
      );
      const payload = res.kind === 'ok' ? res.data?.markNotificationRead : undefined;
      const problems = (payload?.problems ?? []) as PayloadProblem[];
      if (res.kind !== 'ok' || !payload) {
        this.toasts.error('Could not mark this read — it stays unread. Try again.');
      } else if (problems.length > 0) {
        this.toasts.error(problems[0].message);
      } else {
        this.toasts.ok('Marked read.');
      }
      void this.store.refresh(); // reload the truth either way
    } finally {
      this.busy.set(false);
    }
  }

  private fetchPage(after: string | null): Promise<GqlResult<Page<InboxItem>>> {
    return this.gql
      .query(
        NotificationsDocument,
        { unreadOnly: this.unreadOnly(), first: 25, after },
        this.abort.signal,
      )
      .then((res) => {
        if (res.kind !== 'ok') return res;
        if (!res.data) return { kind: 'ok', data: null, errors: res.errors };
        const conn = res.data.notifications;
        return {
          kind: 'ok',
          errors: res.errors,
          data: {
            items: conn.items,
            endCursor: conn.pageInfo.endCursor,
            hasNextPage: conn.pageInfo.hasNextPage,
          },
        };
      });
  }
}
