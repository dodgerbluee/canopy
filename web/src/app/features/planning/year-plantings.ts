import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  effect,
  inject,
  input,
  signal,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { PlanningApi, monthIndex, type PlanRow } from './planning-data';

@Component({
  selector: 'year-plantings',
  imports: [FormsModule, RouterLink],
  providers: [PlanningApi],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './planning-integrations.css',
  template: `<section class="year-plantings" aria-label="Planned plantings overlay">
    <label class="planning-overlay-toggle"
      ><input type="checkbox" [ngModel]="included()" (ngModelChange)="included.set($event)" />
      Include planned plantings <span class="caption">Future intentions</span></label
    >
    @if (included()) {
      <p class="caption">
        Dashed bands are your chosen windows, separate from owned plants and recorded moments.
        Source suggestions remain in Planning.
      </p>
      @if (loading()) {
        <p role="status">Loading planned windows…</p>
      } @else if (error()) {
        <p role="alert">{{ error() }}</p>
        <button class="btn" type="button" (click)="load()">Retry planned windows</button>
      } @else {
        <div class="planned-year-axis" aria-hidden="true">
          <span></span>
          <div>
            @for (m of months; track m) {
              <span>{{ m }}</span>
            }
          </div>
        </div>
        @for (row of rows(); track row.plan.id) {
          <a class="planned-year-row" routerLink="/planning" [queryParams]="{ plan: row.plan.id }"
            ><span
              ><strong>{{ row.plan.name }}</strong
              ><small
                >Planned · {{ row.plan.intendedLocation?.name || 'Place undecided' }}</small
              ></span
            >
            <div class="planned-year-track">
              <span
                class="planned-year-band"
                [style.grid-column]="column(row)"
                [attr.title]="row.plan.name + ' · chosen window'"
                ><span class="visually-hidden">Chosen window</span></span
              >
            </div></a
          >
        }
        @if (!rows().length) {
          <p class="caption">
            No chosen planting windows overlap {{ year() }}. Undated ideas stay in Planning.
          </p>
        }
        <a class="btn btn-ghost" routerLink="/planning">Open Planning</a>
      }
    }
  </section>`,
})
export class YearPlantings {
  readonly year = input.required<number>();
  readonly included = signal(false);
  readonly loading = signal(false);
  readonly error = signal('');
  readonly rows = signal<PlanRow[]>([]);
  readonly months = ['J', 'F', 'M', 'A', 'M', 'J', 'J', 'A', 'S', 'O', 'N', 'D'];
  private readonly api = inject(PlanningApi);
  private abort?: AbortController;
  private generation = 0;
  constructor() {
    effect(() => {
      this.year();
      if (this.included()) void this.load();
      else {
        this.generation++;
        this.abort?.abort();
      }
    });
    inject(DestroyRef).onDestroy(() => {
      this.generation++;
      this.abort?.abort();
    });
  }
  async load(): Promise<void> {
    const n = ++this.generation;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.loading.set(true);
    this.error.set('');
    const year = this.year();
    try {
      const rows: PlanRow[] = [];
      const seen = new Set<string>();
      let after: string | null = null;
      for (;;) {
        const page = await this.api.board(
          { fromMonth: year + '-01', toMonth: year + '-12' },
          {},
          after,
          abort.signal,
        );
        rows.push(
          ...page.items.filter((r) => r.schedule.targetVisible && !!r.schedule.targetDisplayMonths),
        );
        if (!page.pageInfo.hasNextPage) break;
        after = page.pageInfo.endCursor;
        if (!after || seen.has(after)) throw new Error('Incomplete planning page');
        seen.add(after);
      }
      if (n === this.generation) this.rows.set(rows);
    } catch {
      if (n === this.generation)
        this.error.set('Planned windows could not be loaded. Recorded garden data is unchanged.');
    } finally {
      if (n === this.generation) this.loading.set(false);
    }
  }
  column(row: PlanRow): string {
    const range = row.schedule.targetDisplayMonths!;
    const start = monthIndex(this.year() + '-01');
    return `${Math.max(1, monthIndex(range.from) - start + 1)} / ${Math.min(13, monthIndex(range.to) - start + 2)}`;
  }
}
