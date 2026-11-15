import { ChangeDetectionStrategy, Component, DestroyRef, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { PlantingEventsDocument } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiIcon } from '../../ui/icon';
import {
  calendarWorkspace,
  civilDate,
  dayKey,
  resultData,
  workspaceDay,
} from '../calendar/calendar-data';
import { dateLabel } from './planning-data';
import type { PlanningEvent } from './planning-events';

@Component({
  selector: 'upcoming-plantings',
  imports: [RouterLink, UiIcon],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './planning-integrations.css',
  template: `<section class="planning-upcoming card" aria-labelledby="upcoming-plantings-title">
    <div class="integration-heading">
      <div>
        <p class="kicker">LOOKING AHEAD</p>
        <h2 id="upcoming-plantings-title">Upcoming plantings</h2>
      </div>
      <a class="btn btn-ghost" routerLink="/planning"
        >Planning <ui-icon name="arrow" [size]="16"
      /></a>
    </div>
    <p class="caption">Your chosen actions for the next seven days. Garden care stays separate.</p>
    @if (loading()) {
      <p role="status" class="caption">Loading plans…</p>
    } @else if (error()) {
      <p class="caption" role="alert">{{ error() }}</p>
      <button class="btn" type="button" (click)="load()">Retry planned actions</button>
    } @else {
      @for (item of items(); track item.id) {
        <a class="integration-action" routerLink="/planning" [queryParams]="{ plan: item.planId }"
          ><ui-icon name="sprout" [size]="19" /><span
            ><strong>{{ item.title }}</strong
            ><small>{{ item.planName }}</small></span
          ><time [attr.datetime]="item.day">{{ dateLabel(item.day) }}</time></a
        >
      }
      @if (!items().length) {
        <p class="caption">
          No open planting actions in this window. Ideas and seasonal windows are in Planning.
        </p>
      }
      @if (more()) {
        <p class="caption">More actions are available in Planning.</p>
      }
    }
  </section>`,
})
export class UpcomingPlantings {
  private readonly gql = inject(GraphQLClient);
  private abort?: AbortController;
  private generation = 0;
  readonly items = signal<PlanningEvent[]>([]);
  readonly loading = signal(true);
  readonly error = signal('');
  readonly more = signal(false);
  readonly dateLabel = dateLabel;
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.generation++;
      this.abort?.abort();
    });
    void this.load();
  }
  async load(): Promise<void> {
    const n = ++this.generation;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.loading.set(true);
    this.error.set('');
    try {
      const ws = await calendarWorkspace(this.gql, abort.signal);
      const from = workspaceDay(new Date(), ws.timezone);
      const end = civilDate(from);
      end.setUTCDate(end.getUTCDate() + 7);
      const page = resultData(
        await this.gql.query(
          PlantingEventsDocument,
          { from, to: dayKey(end), first: 100 },
          abort.signal,
        ),
      ).plantingEvents;
      if (n !== this.generation) return;
      const open = page.items.filter((i) => i.state === 'OPEN');
      this.items.set(open.slice(0, 6));
      this.more.set(page.pageInfo.hasNextPage || open.length > 6);
    } catch {
      if (n === this.generation)
        this.error.set('Planned actions are unavailable. Your garden records are unchanged.');
    } finally {
      if (n === this.generation) this.loading.set(false);
    }
  }
}
