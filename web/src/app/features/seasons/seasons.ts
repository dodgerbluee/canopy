import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { GardenKnowledgeDocument, RefreshGardenKnowledgeDocument } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon, type UiIconName } from '../../ui/icon';
import { resultData } from '../calendar/calendar-data';
import { GardenOverview } from './garden-overview';
import { GardenChatDialog } from '../assistant/garden-chat';
import { GardenSettings } from './garden-settings';
import {
  seasonalKinds as kinds,
  laneCount,
  requestKey,
  seasonalSegments,
  type Garden,
  type GardenPlant,
  type SeasonalFact,
} from './seasons-data';
@Component({
  selector: 'garden-seasons',
  imports: [
    FormsModule,
    RouterLink,
    UiIcon,
    UiEntryDialog,
    GardenSettings,

    GardenOverview,
    GardenChatDialog,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './seasons.html',
  styleUrls: [
    './seasons.css',
    './seasons-tracks.css',
    './seasons-kinds.css',
    './seasons-dialogs.css',
    './seasons-responsive.css',
  ],
})
export class SeasonsPage {
  private readonly gql = inject(GraphQLClient);
  private alive = true;
  private read?: AbortController;
  private generation = 0;
  private timer?: ReturnType<typeof setTimeout>;
  readonly settingsPanel = viewChild<GardenSettings>('settingsPanel');
  readonly detailDialog = viewChild<UiEntryDialog>('detailDialog');
  readonly garden = signal<Garden | null>(null);
  readonly year = signal(new Date().getFullYear());
  readonly loading = signal(true);
  readonly error = signal('');
  readonly search = signal('');
  readonly selectedKinds = signal(new Set<string>(kinds.map((k) => k.kind)));
  readonly kinds = kinds;
  readonly stateLabels: Record<GardenPlant['state'], string> = {
    READY: 'Guidance ready',
    PENDING: 'Queued for research',
    PROCESSING: 'Reading sources…',
    NEEDS_LOCATION: 'Set growing location',
    DISABLED: 'Guidance is off',
    UNSUPPORTED: 'No regional source yet',
    FAILED: 'Retry needed',
  };
  readonly months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];
  readonly selectedPlant = signal<GardenPlant | null>(null);
  readonly selectedFact = signal<SeasonalFact | null>(null);
  readonly refreshing = signal(false);
  readonly refreshError = signal('');
  private readonly refreshKeys = new Map<string, string>();
  readonly chatDialog = viewChild<GardenChatDialog>('chatDialog');
  readonly chatOpen = signal(false);
  readonly brokenImages = signal(new Set<string>());
  readonly ready = computed(
    () => this.garden()?.plants.filter((p) => p.state === 'READY').length ?? 0,
  );
  readonly rows = computed(
    () =>
      this.garden()
        ?.plants.filter((p) => p.name.toLowerCase().includes(this.search().toLowerCase()))
        .map((plant) => {
          const segments = seasonalSegments(
            plant.facts.filter((f) => this.selectedKinds().has(f.kind)),
          );
          return {
            plant,
            segments,
            lanes: laneCount(segments),
            untimed: plant.facts.filter((f) => !f.months.length).length,
          };
        }) ?? [],
  );
  readonly overviewPlants = computed(() => this.rows().map((row) => row.plant));
  readonly activityCount = computed(
    () =>
      this.garden()?.records.reduce(
        (sum, r) => sum + r.waterings + r.photos + r.harvests + r.fertilizations,
        0,
      ) ?? 0,
  );
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.alive = false;
      this.read?.abort();
      clearTimeout(this.timer);
    });
    void this.load();
  }
  async load(background = false): Promise<void> {
    const n = ++this.generation;
    this.read?.abort();
    const request = (this.read = new AbortController());
    clearTimeout(this.timer);
    if (!background) this.loading.set(true);
    this.error.set('');
    try {
      const garden = resultData(
        await this.gql.query(GardenKnowledgeDocument, { year: this.year() }, request.signal),
      ).gardenKnowledge;
      if (!this.alive || n !== this.generation) return;
      this.garden.set(garden);
      if (garden.plants.some((p) => p.state === 'PENDING' || p.state === 'PROCESSING'))
        this.timer = setTimeout(() => {
          if (document.visibilityState !== 'hidden') void this.load(true);
          else this.timer = setTimeout(() => void this.load(true), 10000);
        }, 5000);
    } catch (e) {
      if (this.alive && n === this.generation)
        this.error.set(e instanceof Error ? e.message : 'Your garden year could not be loaded.');
    } finally {
      if (this.alive && n === this.generation) this.loading.set(false);
    }
  }
  moveYear(offset: number): void {
    const year = this.year() + offset;
    if (year < 1900 || year > 2200) return;
    this.year.set(year);
    this.chatDialog()?.reset();
    void this.load();
  }
  toggle(kind: string): void {
    this.selectedKinds.update((set) => {
      const next = new Set(set);
      next.has(kind) ? next.delete(kind) : next.add(kind);
      return next;
    });
  }
  icon(kind: string): UiIconName {
    return kinds.find((k) => k.kind === kind)?.icon ?? 'leaf';
  }
  timing(fact: SeasonalFact): string {
    if (!fact.months.length) return 'Timing depends on conditions';
    const names = fact.months.map((m) => this.months[m - 1]).join(', ');
    return (fact.timingBasis === 'SEASON' ? 'Approximate season: ' : 'Source months: ') + names;
  }
  openFact(plant: GardenPlant, fact: SeasonalFact | null): void {
    this.selectedPlant.set(plant);
    this.selectedFact.set(fact);
  }
  closeDetail(): void {
    this.selectedPlant.set(null);
    this.selectedFact.set(null);
  }
  focusBand(event: FocusEvent): void {
    const band = event.currentTarget as HTMLElement;
    const region = band.closest<HTMLElement>('.season-scroll');
    const identity = band.closest('.season-row')?.querySelector<HTMLElement>('.season-plant');
    if (!region || !identity) return;
    const left = band.getBoundingClientRect().left;
    const minimum = identity.getBoundingClientRect().right + 8;
    const maximum = region.getBoundingClientRect().right - 20;
    if (left < minimum || left > maximum) region.scrollLeft += left - minimum;
  }
  imageFailed(id: string): void {
    this.brokenImages.update((ids) => new Set([...ids, id]));
  }
  recordSummary(id: string): string {
    const records = this.garden()?.records.filter((r) => r.plantId === id) ?? [];
    const water = records.reduce((n, r) => n + r.waterings, 0),
      photos = records.reduce((n, r) => n + r.photos, 0),
      harvests = records.reduce((n, r) => n + r.harvests, 0);
    return `${water} waterings · ${photos} photos · ${harvests} harvest logs`;
  }
  async refresh(plantId?: string): Promise<void> {
    if (this.refreshing()) return;
    this.refreshing.set(true);
    this.refreshError.set('');
    const signature = plantId ?? 'all';
    let key = this.refreshKeys.get(signature);
    if (!key) {
      key = requestKey();
      this.refreshKeys.set(signature, key);
    }
    try {
      const payload = resultData(
        await this.gql.mutate(RefreshGardenKnowledgeDocument, {
          plantId: plantId ?? null,
          clientMutationId: key,
        }),
      ).refreshGardenKnowledge;
      if (payload.problems.length)
        throw new Error(payload.problems.map((p) => p.message).join(' '));
      if (!this.alive) return;
      this.refreshKeys.delete(signature);
      this.closeDetail();
      await this.load(true);
    } catch (e) {
      if (this.alive)
        this.refreshError.set(e instanceof Error ? e.message : 'Guidance could not be refreshed.');
    } finally {
      if (this.alive) this.refreshing.set(false);
    }
  }
  settingsSaved(): void {
    this.closeDetail();
    this.chatDialog()?.reset();
    void this.load(true);
  }
}
