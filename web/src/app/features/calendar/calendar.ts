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
import { RecordRainDocument, VoidRainDocument, type PlantFieldsFragment } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { SessionService } from '../../core/auth/session';
import { UiIcon, type UiIconName } from '../../ui/icon';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiToasts } from '../../ui/toast';
import {
  calendarEvents,
  calendarPlants,
  calendarWorkspace,
  civilDate,
  monthDays,
  mutationKey,
  rainMillimeters,
  resultData,
  shiftMonth,
  workspaceDay,
  type CalendarItem,
} from './calendar-data';

@Component({
  selector: 'app-calendar',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [FormsModule, RouterLink, UiIcon, UiEntryDialog],
  templateUrl: './calendar.html',
  styleUrls: [
    './calendar.css',
    './calendar-filters.css',
    './calendar-details.css',
    './calendar-responsive.css',
  ],
})
export class CalendarPage {
  private readonly gql = inject(GraphQLClient);
  private readonly session = inject(SessionService);
  private readonly toasts = inject(UiToasts);
  readonly rainDialog = viewChild<UiEntryDialog>('rainDialog');
  readonly filterDialog = viewChild<UiEntryDialog>('filterDialog');
  readonly removeDialog = viewChild<UiEntryDialog>('removeDialog');
  readonly brokenImages = signal(new Set<string>());
  readonly plants = signal<PlantFieldsFragment[]>([]);
  readonly events = signal<CalendarItem[]>([]);
  readonly selectedPlants = signal(new Set<string>());
  readonly month = signal('');
  readonly selectedDay = signal('');
  readonly today = signal('');
  readonly timezone = signal('');
  readonly area = signal('Your garden');
  readonly loading = signal(true);
  readonly error = signal('');
  readonly search = signal('');
  readonly showRain = signal(true);
  readonly showWater = signal(true);
  readonly showPhotos = signal(true);
  readonly showOther = signal(true);
  readonly filtersOpen = signal(false);
  readonly rainOpen = signal(false);
  readonly saving = signal(false);
  readonly saveError = signal('');
  readonly rainDay = signal('');
  readonly rainAmount = signal('');
  readonly rainUnit = signal<'mm' | 'in'>('in');
  readonly rainNote = signal('');
  readonly removeTarget = signal<CalendarItem | null>(null);
  readonly removeError = signal('');
  private readonly keys = new Map<string, string>();
  private abort?: AbortController;
  private request = 0;
  private destroyed = false;
  readonly days = computed(() => (this.month() ? monthDays(this.month()) : []));
  readonly monthLabel = computed(() =>
    this.month()
      ? new Intl.DateTimeFormat('en-US', {
          month: 'long',
          year: 'numeric',
          timeZone: 'UTC',
        }).format(civilDate(this.month() + '-01'))
      : 'Your calendar',
  );
  readonly selectedLabel = computed(() =>
    this.selectedDay() ? this.dateLabel(this.selectedDay()) : 'Select a day',
  );
  readonly filteredPlants = computed(() =>
    this.plants().filter((p) => p.name.toLowerCase().includes(this.search().toLowerCase())),
  );
  readonly visible = computed(() =>
    this.events().filter((e) => {
      if (e.kind === 'RAIN') return this.showRain();
      if (!e.plantId || !this.selectedPlants().has(e.plantId)) return false;
      if (e.kind === 'PHOTO') return this.showPhotos();
      if (e.activityKind === 'WATER') return this.showWater();
      return this.showOther();
    }),
  );
  readonly byDay = computed(() => {
    const map = new Map<string, CalendarItem[]>();
    for (const e of this.visible()) map.set(e.day, [...(map.get(e.day) ?? []), e]);
    return map;
  });
  readonly dayEvents = computed(() => this.byDay().get(this.selectedDay()) ?? []);
  readonly monthCount = computed(
    () => this.visible().filter((e) => e.day.startsWith(this.month())).length,
  );
  readonly weekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.destroyed = true;
      this.abort?.abort();
    });
    void this.initialize();
  }
  async initialize(): Promise<void> {
    const n = ++this.request;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.loading.set(true);
    this.error.set('');
    try {
      const [ws, plants] = await Promise.all([
        calendarWorkspace(this.gql, abort.signal),
        calendarPlants(this.gql, abort.signal),
      ]);
      if (n !== this.request || this.destroyed) return;
      const today = workspaceDay(new Date(), ws.timezone);
      this.timezone.set(ws.timezone);
      this.today.set(today);
      this.area.set(ws.location?.label || ws.displayName || 'Your garden');
      this.plants.set(plants);
      this.selectedPlants.set(new Set(plants.map((p) => p.id)));
      this.month.set(today.slice(0, 7));
      this.selectedDay.set(today);
      await this.loadMonth();
    } catch (e) {
      if (n === this.request && !this.destroyed) {
        this.error.set(this.message(e));
        this.loading.set(false);
      }
    }
  }
  async loadMonth(): Promise<void> {
    if (!this.month()) {
      await this.initialize();
      return;
    }
    const n = ++this.request;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.loading.set(true);
    this.error.set('');
    this.events.set([]);
    const days = this.days();
    try {
      const events = await calendarEvents(this.gql, days[0], days[days.length - 1], abort.signal);
      if (n === this.request && !this.destroyed) this.events.set(events);
    } catch (e) {
      if (n === this.request && !this.destroyed) this.error.set(this.message(e));
    } finally {
      if (n === this.request && !this.destroyed) this.loading.set(false);
    }
  }
  moveMonth(offset: number): void {
    this.month.set(shiftMonth(this.month(), offset));
    this.selectedDay.set(this.month() + '-01');
    void this.loadMonth();
  }
  goToday(): void {
    const day = workspaceDay(new Date(), this.timezone());
    this.today.set(day);
    this.selectedDay.set(day);
    if (this.month() !== day.slice(0, 7)) {
      this.month.set(day.slice(0, 7));
      void this.loadMonth();
    }
  }
  togglePlant(id: string): void {
    this.selectedPlants.update((ids) => {
      const next = new Set(ids);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }
  chooseAll(all: boolean): void {
    this.selectedPlants.set(new Set(all ? this.plants().map((p) => p.id) : []));
  }
  thumbnail(plant: PlantFieldsFragment): string | null {
    return plant.coverPhoto && !this.brokenImages().has(plant.id)
      ? `/api/media/${plant.coverPhoto.asset.id}/medium`
      : null;
  }
  imageFailed(id: string): void {
    this.brokenImages.update((ids) => new Set([...ids, id]));
  }
  plantName(id: string | null): string {
    return this.plants().find((p) => p.id === id)?.name ?? 'Plant';
  }
  dateLabel(day: string): string {
    return new Intl.DateTimeFormat('en-US', {
      weekday: 'long',
      month: 'long',
      day: 'numeric',
      year: 'numeric',
      timeZone: 'UTC',
    }).format(civilDate(day));
  }
  dayNumber(day: string): number {
    return Number(day.slice(-2));
  }
  icon(e: CalendarItem): UiIconName {
    return e.kind === 'RAIN'
      ? 'rain'
      : e.kind === 'PHOTO'
        ? 'camera'
        : e.activityKind === 'WATER'
          ? 'drop'
          : e.kind === 'PURCHASE'
            ? 'receipt'
            : e.activityKind === 'FERTILIZE'
              ? 'sprout'
              : 'leaf';
  }
  groups(day: string): { icon: UiIconName; label: string; count: number }[] {
    const items = this.byDay().get(day) ?? [];
    const kinds: [UiIconName, string, (e: CalendarItem) => boolean][] = [
      ['drop', 'Watering', (e) => e.activityKind === 'WATER'],
      ['rain', 'Rain', (e) => e.kind === 'RAIN'],
      ['camera', 'Photos taken', (e) => e.kind === 'PHOTO'],
      [
        'leaf',
        'Other entries',
        (e) => e.kind !== 'RAIN' && e.kind !== 'PHOTO' && e.activityKind !== 'WATER',
      ],
    ];
    return kinds
      .map(([icon, label, match]) => ({ icon, label, count: items.filter(match).length }))
      .filter((g) => g.count);
  }
  addRain(): void {
    this.keys.clear();
    this.rainDay.set(this.selectedDay());
    this.rainAmount.set('');
    this.rainNote.set('');
    this.saveError.set('');
    this.rainOpen.set(true);
  }
  private key(input: unknown): string {
    const signature = JSON.stringify(input);
    let key = this.keys.get(signature);
    if (!key) {
      key = mutationKey();
      this.keys.set(signature, key);
    }
    return key;
  }
  async saveRain(): Promise<void> {
    if (this.saving()) return;
    let amountMm: string | null;
    try {
      amountMm = rainMillimeters(this.rainAmount(), this.rainUnit());
    } catch (e) {
      this.saveError.set(this.message(e));
      return;
    }
    const day = this.rainDay();
    if (
      !/^\d{4}-\d{2}-\d{2}$/.test(day) ||
      Number.isNaN(civilDate(day).getTime()) ||
      civilDate(day).toISOString().slice(0, 10) !== day
    ) {
      this.saveError.set('Choose a valid date.');
      return;
    }
    const input = { day, amountMm, note: this.rainNote().trim() || null };
    const workspace = this.session.info()?.workspaceId;
    this.saving.set(true);
    this.saveError.set('');
    try {
      const payload = resultData(
        await this.gql.mutate(RecordRainDocument, {
          input,
          clientMutationId: this.key({ workspace, operation: 'rain', input }),
        }),
      ).recordRain;
      if (this.destroyed || workspace !== this.session.info()?.workspaceId) return;
      if (payload.problems.length)
        throw new Error(payload.problems.map((p) => p.message).join(' '));
      if (!payload.rain)
        throw new Error('The save could not be confirmed. Retry with these same values.');
      const rain = payload.rain;
      this.events.update((es) => [
        ...es.filter((e) => e.id !== 'rain:' + rain.id),
        {
          id: 'rain:' + rain.id,
          entityId: rain.id,
          plantId: null,
          day: rain.day,
          kind: 'RAIN',
          activityKind: null,
          title: 'Rain recorded',
          amountMm: rain.amountMm,
          note: rain.note,
          revision: rain.revision,
        },
      ]);
      this.rainDialog()?.markPristine();
      this.rainOpen.set(false);
      this.toasts.ok('Rain added to your calendar.');
      this.selectedDay.set(day);
      if (!day.startsWith(this.month())) {
        this.month.set(day.slice(0, 7));
      }
      void this.loadMonth();
    } catch (e) {
      if (!this.destroyed)
        this.saveError.set(
          this.message(e) + ' Your draft is still here; retrying the same values is safe.',
        );
    } finally {
      if (!this.destroyed) this.saving.set(false);
    }
  }
  confirmRemove(e: CalendarItem): void {
    this.removeError.set('');
    this.removeTarget.set(e);
  }
  async removeRain(): Promise<void> {
    const target = this.removeTarget();
    if (!target || !target.revision || this.saving()) return;
    this.saving.set(true);
    this.removeError.set('');
    const workspace = this.session.info()?.workspaceId;
    const input = { id: target.entityId, expectedRevision: target.revision };
    try {
      const payload = resultData(
        await this.gql.mutate(VoidRainDocument, {
          input,
          clientMutationId: this.key({ workspace, operation: 'remove', input }),
        }),
      ).voidRain;
      if (this.destroyed || workspace !== this.session.info()?.workspaceId) return;
      if (payload.problems.length)
        throw new Error(payload.problems.map((p) => p.message).join(' '));
      if (payload.removedId !== target.entityId)
        throw new Error('Removal could not be confirmed. Retry safely.');
      this.events.update((es) => es.filter((e) => e.id !== target.id));
      this.removeDialog()?.markPristine();
      this.removeTarget.set(null);
      this.toasts.ok('Rain record removed.');
    } catch (e) {
      if (!this.destroyed) this.removeError.set(this.message(e));
    } finally {
      if (!this.destroyed) this.saving.set(false);
    }
  }
  private message(error: unknown): string {
    return error instanceof Error ? error.message : 'Something went wrong. Please retry.';
  }
}
