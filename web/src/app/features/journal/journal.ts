import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  signal,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { type PlantFieldsFragment } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { loadGardenPlants } from '../assistant/plants';
import { JournalTimeline } from './journal-timeline';

/**
 * Workspace journal (T19, restyled by UI01 pass C; page width owned here as of
 * UI03): the whole-garden timeline. The quick log and the type filters live
 * inside the timeline component itself — the same component the plant detail's
 * Journey tab embeds, scoped by input. The page column cap belongs to this
 * page only; the shared timeline never carries a page width, so the plant
 * detail's Journey tab keeps inheriting its available width.
 */
@Component({
  selector: 'journal-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [JournalTimeline, RouterLink, FormsModule, UiEntryDialog],
  template: `
    <div class="page-col">
      <header class="page-head">
        <p class="eyebrow">The garden, in order</p>
        <h1 class="editorial">Journal</h1>
        <p class="lede">
          Every note, activity, measurement and photo across the workspace — kept in the order it
          happened.
        </p>
      </header>
      <section class="journal-context card">
        <div>
          <strong>Your garden’s memory</strong>
          <p class="caption">Look back at what changed, what you did, and how each plant grew.</p>
        </div>
        <div class="journal-tools">
          <button class="btn" (click)="pickerOpen.set(true)">{{ selectedName() }}</button
          ><a class="btn" routerLink="/calendar">See dates</a
          ><a class="btn" routerLink="/rounds">Photo rounds</a>
        </div>
      </section>
      @if (plantError()) {
        <p class="caption" role="alert">
          {{ plantError() }} <button class="btn" (click)="loadPlants()">Retry plant filter</button>
        </p>
      }
      @if (selected()) {
        <p class="caption">
          Showing {{ selectedName() }}.
          <a [routerLink]="['/plants', selected()]" [queryParams]="{ tab: 'details' }"
            >Open photos and compare growth</a
          >
        </p>
      }
      @for (scope of [selected()]; track scope) {
        <journal-timeline [plantId]="scope" />
      }
      <ui-entry-dialog
        [open]="pickerOpen()"
        title="Choose a journal"
        description="Your full garden history or one plant’s story."
        [dirty]="false"
        (dismissed)="pickerOpen.set(false)"
      >
        <input
          class="input"
          type="search"
          aria-label="Find a plant journal"
          placeholder="Find a plant…"
          [ngModel]="search()"
          (ngModelChange)="search.set($event)"
        />
        <div class="journal-picks">
          <button class="btn" (click)="choose(null)">Whole garden</button>
          @for (plant of filtered(); track plant.id) {
            <button class="btn" (click)="choose(plant.id)">
              {{ plant.name }}{{ plant.status === 'ARCHIVED' ? ' · Archived' : '' }}
            </button>
          }
        </div>
      </ui-entry-dialog>
    </div>
  `,
  styles: [
    `
      :host {
        display: block;
      }
      /* Page width lives here, not in the shared timeline and not globally.
         Full available width on phone/tablet, readable editorial cap on wide
         screens (the shell gutter already keeps the gutters in). */
      .page-col {
        width: 100%;
        max-width: 960px;
      }
      /* eyebrow / title / description read as one group: tight inside,
         no ad-hoc inline margins. */
      .page-head {
        display: grid;
        gap: var(--sp-2);
        margin: 0 0 var(--sp-6);
      }
      .page-head .eyebrow {
        margin: 0;
      }
      .page-head h1 {
        margin: 0;
      }
      .page-head .lede {
        margin: 0;
        max-width: 60ch;
        font-size: var(--fs-small);
        line-height: var(--lh-small);
        color: var(--text-muted);
      }
      .journal-context {
        display: flex;
        gap: 16px;
        align-items: center;
        justify-content: space-between;
        flex-wrap: wrap;
        margin-bottom: 22px;
      }
      .journal-context p {
        margin: 6px 0 0;
      }
      .journal-tools {
        display: flex;
        gap: 8px;
        flex-wrap: wrap;
      }
      .journal-picks {
        display: grid;
        gap: 10px;
        margin-top: 16px;
      }
      .journal-picks button {
        text-align: left;
        justify-content: flex-start;
      }
      @media (min-width: 1000px) {
        .page-head {
          margin-bottom: var(--sp-8);
        }
      }
    `,
  ],
})
export class JournalPage {
  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  readonly plants = signal<PlantFieldsFragment[]>([]);
  readonly selected = signal<string | null>(null);
  readonly pickerOpen = signal(false);
  readonly search = signal('');
  readonly plantError = signal('');
  readonly filtered = computed(() =>
    this.plants().filter((p) => p.name.toLowerCase().includes(this.search().toLowerCase())),
  );
  readonly selectedName = computed(
    () => this.plants().find((p) => p.id === this.selected())?.name ?? 'Whole garden',
  );
  constructor() {
    inject(DestroyRef).onDestroy(() => this.abort.abort());
    void this.loadPlants();
  }
  async loadPlants(): Promise<void> {
    this.plantError.set('');
    try {
      const plants = await loadGardenPlants(this.gql, this.abort.signal, true);
      if (!this.abort.signal.aborted) this.plants.set(plants);
    } catch {
      if (!this.abort.signal.aborted)
        this.plantError.set(
          'The plant filter could not be loaded. Your whole-garden journal is still available.',
        );
    }
  }
  choose(id: string | null): void {
    this.pickerOpen.set(false);
    this.selected.set(id);
  }
}
