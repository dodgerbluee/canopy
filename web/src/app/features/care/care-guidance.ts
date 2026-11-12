import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  signal,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import { GardenKnowledgeDocument } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiIcon } from '../../ui/icon';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { resultData } from '../calendar/calendar-data';
import {
  gardenMonths,
  seasonalKinds,
  type Garden,
  type GardenWindow,
} from '../seasons/seasons-data';

@Component({
  selector: 'care-seasonal-guidance',
  imports: [RouterLink, UiIcon, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './care-guidance.html',
  styleUrls: ['./care-guidance.css', '../seasons/seasons-kinds.css'],
})
export class CareGuidance {
  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  readonly garden = signal<Garden | null>(null);
  readonly error = signal('');
  readonly loading = signal(true);
  readonly selected = signal<GardenWindow | null>(null);
  private readonly careKinds = new Set(['FERTILIZE', 'WATER_CHECK', 'PRUNE', 'PROTECT', 'HARVEST']);
  readonly month = computed(() =>
    Number(
      new Intl.DateTimeFormat('en-US', {
        timeZone: this.garden()?.settings.timezone ?? 'UTC',
        month: 'numeric',
      }).format(new Date()),
    ),
  );
  readonly groups = computed(
    () => gardenMonths(this.garden()?.plants ?? [], this.careKinds)[this.month() - 1].groups,
  );
  readonly monthName = computed(() =>
    new Intl.DateTimeFormat('en-US', {
      timeZone: this.garden()?.settings.timezone ?? 'UTC',
      month: 'long',
    }).format(new Date()),
  );
  readonly checks = computed(() => {
    const checks: GardenWindow[] = [];
    for (const plant of this.garden()?.plants ?? [])
      for (const fact of plant.facts)
        if (!fact.months.length && (fact.kind === 'WATER_CHECK' || fact.kind === 'PROTECT'))
          checks.push({ plant, fact });
    return checks;
  });
  constructor() {
    inject(DestroyRef).onDestroy(() => this.abort.abort());
    void this.load();
  }
  async load(): Promise<void> {
    this.loading.set(true);
    this.error.set('');
    try {
      const g = resultData(
        await this.gql.query(
          GardenKnowledgeDocument,
          { year: new Date().getFullYear() },
          this.abort.signal,
        ),
      ).gardenKnowledge;
      if (!this.abort.signal.aborted) this.garden.set(g);
    } catch {
      if (!this.abort.signal.aborted)
        this.error.set('Seasonal guidance could not be loaded. Scheduled care above is separate.');
    } finally {
      if (!this.abort.signal.aborted) this.loading.set(false);
    }
  }
  icon(kind: string) {
    return seasonalKinds.find((k) => k.kind === kind)?.icon ?? 'leaf';
  }
}
