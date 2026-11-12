import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  GardenPlacesDocument,
  GardenKnowledgeDocument,
  SaveGardenKnowledgeSettingsDocument,
  type GardenPlacesQuery,
} from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon } from '../../ui/icon';
import { resultData } from '../calendar/calendar-data';
import { requestKey, type Garden } from './seasons-data';
type Settings = Garden['settings'];
@Component({
  selector: 'garden-knowledge-settings',
  imports: [FormsModule, UiEntryDialog, UiIcon],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './garden-settings.html',
  styleUrl: './garden-settings.css',
})
export class GardenSettings {
  private readonly gql = inject(GraphQLClient);
  readonly settings = input<Settings | null>(null);
  readonly saved = output<void>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly open = signal(false);
  readonly busy = signal(false);
  readonly error = signal('');
  readonly loading = signal(false);
  readonly search = signal('');
  readonly places = signal<GardenPlacesQuery['gardenPlaces']>([]);
  readonly searching = signal(false);
  readonly searchError = signal('');
  readonly country = signal('');
  readonly region = signal('');
  readonly place = signal('');
  readonly timezone = signal('');
  readonly latitude = signal('');
  readonly longitude = signal('');
  readonly zone = signal('');
  readonly zoneSystem = signal('USDA');
  readonly enabled = signal(false);
  private revision = 0;
  private alive = true;
  private opening = 0;
  private searchRequest = 0;
  private readonly keys = new Map<string, string>();
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.alive = false;
      ++this.opening;
      ++this.searchRequest;
    });
  }
  async edit(): Promise<void> {
    const n = ++this.opening;
    this.loading.set(true);
    this.error.set('');
    this.searchError.set('');
    this.places.set([]);
    this.search.set('');
    this.keys.clear();
    try {
      const settings =
        this.settings() ??
        resultData(
          await this.gql.query(GardenKnowledgeDocument, { year: new Date().getFullYear() }),
        ).gardenKnowledge.settings;
      if (!this.alive || n !== this.opening) return;
      this.country.set(settings.country);
      this.region.set(settings.region);
      this.place.set(settings.place);
      this.timezone.set(settings.timezone);
      this.latitude.set(settings.latitude ?? '');
      this.longitude.set(settings.longitude ?? '');
      this.zone.set(settings.hardinessZone ?? '');
      this.zoneSystem.set(settings.zoneSystem ?? 'USDA');
      this.enabled.set(settings.enabled);
      this.revision = settings.revision;
      this.open.set(true);
    } catch (e) {
      if (this.alive)
        this.error.set(e instanceof Error ? e.message : 'Garden settings could not be loaded.');
    } finally {
      if (this.alive) this.loading.set(false);
    }
  }
  async find(): Promise<void> {
    const n = ++this.searchRequest;
    this.searching.set(true);
    this.searchError.set('');
    this.places.set([]);
    try {
      const places = resultData(
        await this.gql.query(GardenPlacesDocument, { search: this.search() }),
      ).gardenPlaces;
      if (this.alive && n === this.searchRequest) {
        this.places.set(places);
        if (!places.length)
          this.searchError.set(
            'No matching places. Try a city and country, or enter the fields below.',
          );
      }
    } catch (e) {
      if (this.alive && n === this.searchRequest)
        this.searchError.set(e instanceof Error ? e.message : 'Location search failed.');
    } finally {
      if (this.alive && n === this.searchRequest) this.searching.set(false);
    }
  }
  select(p: GardenPlacesQuery['gardenPlaces'][number]): void {
    this.country.set(p.country);
    this.region.set(p.region);
    this.place.set(p.name);
    this.timezone.set(p.timezone);
    this.latitude.set(Number(p.latitude).toFixed(3));
    this.longitude.set(Number(p.longitude).toFixed(3));
    this.places.set([]);
    this.entry()?.markDirty();
  }
  geography(field: 'country' | 'region' | 'place', value: string): void {
    this[field].set(value);
    this.latitude.set('');
    this.longitude.set('');
  }
  device(): void {
    if (!navigator.geolocation) {
      this.searchError.set(
        'Device location is unavailable. Use city search or enter the location manually.',
      );
      return;
    }
    const n = this.opening;
    navigator.geolocation.getCurrentPosition(
      (p) => {
        if (!this.alive || !this.open() || n !== this.opening) return;
        this.latitude.set(p.coords.latitude.toFixed(3));
        this.longitude.set(p.coords.longitude.toFixed(3));
        this.entry()?.markDirty();
      },
      () => {
        if (this.alive)
          this.searchError.set('Device location was not available. Manual location still works.');
      },
      { enableHighAccuracy: false, timeout: 10000, maximumAge: 300000 },
    );
  }
  async save(): Promise<void> {
    if (this.busy()) return;
    this.error.set('');
    const input = {
      country: this.country().trim().toUpperCase(),
      region: this.region().trim(),
      place: this.place().trim(),
      timezone: this.timezone().trim(),
      latitude: this.latitude().trim() || null,
      longitude: this.longitude().trim() || null,
      hardinessZone: this.zone().trim() || null,
      zoneSystem: this.zone().trim() ? this.zoneSystem() : null,
      enabled: this.enabled(),
      expectedRevision: this.revision,
    };
    const signature = JSON.stringify(input);
    let key = this.keys.get(signature);
    if (!key) {
      key = requestKey();
      this.keys.set(signature, key);
    }
    this.busy.set(true);
    try {
      const payload = resultData(
        await this.gql.mutate(SaveGardenKnowledgeSettingsDocument, {
          input,
          clientMutationId: key,
        }),
      ).saveGardenKnowledgeSettings;
      if (!this.alive) return;
      if (payload.problems.length)
        throw new Error(payload.problems.map((p) => p.message).join(' '));
      if (!payload.settings)
        throw new Error('Save could not be confirmed. Retrying these values is safe.');
      this.revision = payload.settings.revision;
      this.entry()?.markPristine();
      this.open.set(false);
      this.saved.emit();
    } catch (e) {
      if (this.alive)
        this.error.set(e instanceof Error ? e.message : 'Garden settings could not be saved.');
    } finally {
      if (this.alive) this.busy.set(false);
    }
  }
}
