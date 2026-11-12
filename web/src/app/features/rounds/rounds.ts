import {
  afterNextRender,
  ElementRef,
  Injector,
  viewChild,
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  OnDestroy,
  OnInit,
  signal,
  effect,
} from '@angular/core';
import { Location } from '@angular/common';
import { toSignal } from '@angular/core/rxjs-interop';
import { map } from 'rxjs';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import type { PlantFieldsFragment } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiIcon } from '../../ui/icon';
import { UiTheme } from '../../ui/theme';
import { SessionService } from '../../core/auth/session';
import { RoundNavigation } from './rounds-navigation';
import { RoundActions, type PhotoJob, type WaterJob } from './rounds-actions';
import { loadRoundHistory, loadRoundPlants, roundDay, type RoundHistory } from './rounds-data';

@Component({
  selector: 'round-page',
  imports: [RouterLink, UiIcon],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './rounds.html',
  styleUrls: ['./rounds.css', './rounds-navigation.css'],
})
export class RoundPage implements OnInit, OnDestroy {
  protected readonly actions = inject(RoundActions);
  private readonly gql = inject(GraphQLClient);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly injector = inject(Injector);
  private readonly plantGrid = viewChild<ElementRef<HTMLElement>>('plantGrid');
  private readonly navigation = inject(RoundNavigation);
  private readonly location = inject(Location);
  private readonly session = inject(SessionService);
  private readonly abort = new AbortController();
  private timer: ReturnType<typeof setInterval> | undefined;
  private historySequence = 0;
  private historyTarget = '';
  private historyRequest: Promise<void> | null = null;
  private capture: { plantId: string; replace: string | null } | null = null;

  protected readonly plants = signal<PlantFieldsFragment[]>([]);
  protected readonly loading = signal(true);
  protected readonly loadError = signal('');
  protected readonly historyLoading = signal(true);
  protected readonly historyError = signal('');
  protected readonly history = signal<RoundHistory | null>(null);
  protected readonly dayKey = signal(roundDay(new Date()).key);
  protected readonly detailId = toSignal(
    this.route.paramMap.pipe(map((params) => params.get('plantId'))),
    {
      initialValue: this.route.snapshot.paramMap.get('plantId'),
    },
  );
  private readonly brokenImages = signal(new Set<string>());

  protected readonly plant = computed(
    () => this.plants().find((p) => p.id === this.detailId()) ?? null,
  );
  protected readonly dateLabel = computed(() =>
    new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'long', day: 'numeric' }).format(
      new Date(`${this.dayKey()}T12:00:00`),
    ),
  );
  protected readonly historyKnown = computed(() => this.history()?.dayKey === this.dayKey());
  protected readonly waterJob = computed(() =>
    this.actions
      .waters()
      .filter((j) => j.plantId === this.plant()?.id && j.dayKey === this.dayKey())
      .at(-1),
  );
  protected readonly photoJob = computed(() =>
    this.actions
      .photos()
      .filter((j) => j.plantId === this.plant()?.id)
      .at(-1),
  );
  protected readonly watered = computed(() =>
    this.plant() ? this.isWatered(this.plant()!.id) : false,
  );
  protected readonly waterDisabled = computed(
    () =>
      !this.historyKnown() ||
      this.historyLoading() ||
      this.watered() ||
      this.waterJob()?.state === 'saving',
  );
  protected readonly waterLabel = computed(() =>
    this.watered()
      ? 'Watered today'
      : this.waterJob()?.state === 'saving'
        ? 'Saving watering…'
        : 'Mark watered',
  );
  protected readonly photoLabel = computed(() =>
    this.photoJob()?.state === 'saving'
      ? 'Saving photo…'
      : this.photoJob()?.state === 'failed'
        ? 'Retake photo'
        : 'Take photo',
  );
  protected readonly photoCount = computed(() => {
    const id = this.plant()?.id;
    if (!id) return 0;
    const ids = new Set(this.historyKnown() ? this.history()?.photos.get(id) : []);
    for (const job of this.actions.photos())
      if (
        job.plantId === id &&
        job.dayKey === this.dayKey() &&
        job.state === 'saved' &&
        job.dateSource !== 'UPLOAD_FALLBACK' &&
        job.photoId
      )
        ids.add(job.photoId);
    return ids.size;
  });
  protected readonly savingPhotos = computed(
    () => this.actions.photos().filter((j) => j.state === 'saving').length,
  );
  protected readonly failures = computed(() =>
    [...this.actions.waters(), ...this.actions.photos()].filter((j) => j.state === 'failed'),
  );
  protected readonly plantChoices = computed(() =>
    this.plants().map((p) => {
      const saved = this.actions
        .photos()
        .filter((j) => j.plantId === p.id && j.state === 'saved')
        .at(-1);
      const src = p.coverPhoto
        ? `/api/media/${p.coverPhoto.asset.id}/medium`
        : saved?.assetId
          ? `/api/media/${saved.assetId}/medium`
          : null;
      return { id: p.id, name: p.name, photo: src && !this.brokenImages().has(src) ? src : null };
    }),
  );
  protected readonly hero = computed(() => {
    const job = this.photoJob(),
      p = this.plant();
    const sources = [
      job?.previewUrl,
      job?.state === 'saved' && job.assetId ? `/api/media/${job.assetId}/display` : null,
      p?.coverPhoto ? `/api/media/${p.coverPhoto.asset.id}/display` : null,
    ];
    return sources.find((src): src is string => !!src && !this.brokenImages().has(src)) ?? null;
  });

  constructor() {
    // A direct full-screen deep link does not construct AppShell.
    inject(UiTheme);
    effect(() => {
      if (
        this.detailId() &&
        this.session.state() === 'anon' &&
        this.session.ended() === 'expired'
      ) {
        void this.router.navigate(['/login'], {
          queryParams: { returnUrl: this.router.url },
          fragment: 'expired',
        });
      }
    });
  }

  ngOnInit(): void {
    void this.loadPlants();
    void this.refreshHistory();
    this.timer = setInterval(this.checkDay, 30_000);
    window.addEventListener('focus', this.checkDay);
  }
  ngOnDestroy(): void {
    this.abort.abort();
    clearInterval(this.timer);
    window.removeEventListener('focus', this.checkDay);
    // Actions live at root: navigating away does not detach a pending photo
    // from its original plant or cancel a save that already reached the server.
  }

  protected async loadPlants(): Promise<void> {
    this.loading.set(true);
    this.loadError.set('');
    try {
      const plants = await loadRoundPlants(this.gql, this.abort.signal);
      if (this.abort.signal.aborted) return;
      this.plants.set(plants);
      this.restoreViewport();
    } catch (error) {
      if (!this.abort.signal.aborted) this.loadError.set(this.message(error));
    } finally {
      if (!this.abort.signal.aborted) this.loading.set(false);
    }
  }

  protected refreshHistory(now = new Date()): Promise<void> {
    const key = roundDay(now).key;
    if (this.historyRequest && this.historyTarget === key) return this.historyRequest;
    const sequence = ++this.historySequence;
    this.historyTarget = key;
    this.dayKey.set(key);
    this.historyLoading.set(true);
    this.historyError.set('');
    const request = loadRoundHistory(this.gql, now, this.abort.signal)
      .then((history) => {
        if (!this.abort.signal.aborted && sequence === this.historySequence)
          this.history.set(history);
      })
      .catch((error) => {
        if (!this.abort.signal.aborted && sequence === this.historySequence) {
          this.history.set(null);
          this.historyError.set(this.message(error));
        }
      })
      .finally(() => {
        if (!this.abort.signal.aborted && sequence === this.historySequence) {
          this.historyLoading.set(false);
          this.historyRequest = null;
        }
      });
    this.historyRequest = request;
    return request;
  }

  protected rememberGrid(id: string): void {
    this.navigation.remember(id);
  }

  protected backToRounds(): void {
    const state = this.location.getState() as { fromRounds?: boolean };
    if (state.fromRounds === true) this.location.back();
    else void this.router.navigateByUrl('/rounds', { replaceUrl: true });
  }

  private restoreViewport(): void {
    const point = this.detailId() ? null : this.navigation.takeReturn();
    afterNextRender(
      () => {
        if (this.abort.signal.aborted) return;
        window.scrollTo({ top: point?.scrollY ?? 0, behavior: 'instant' });
        if (point) {
          const link = [
            ...(this.plantGrid()?.nativeElement.querySelectorAll<HTMLAnchorElement>(
              '[data-plant-id]',
            ) ?? []),
          ].find((element) => element.dataset['plantId'] === point.plantId);
          link?.focus({ preventScroll: true });
        }
      },
      { injector: this.injector },
    );
  }
  protected async water(): Promise<void> {
    const target = this.plant()?.id,
      now = new Date();
    if (!target) return;
    if (roundDay(now).key !== this.dayKey()) await this.refreshHistory(now);
    else if (this.historyRequest) await this.historyRequest;
    if (!this.historyKnown() || this.isWatered(target) || this.abort.signal.aborted) return;
    await this.actions.markWatered(target, now);
  }
  protected openCamera(input: HTMLInputElement): void {
    const p = this.plant(),
      job = this.photoJob();
    if (!p || job?.state === 'saving') return;
    this.capture = { plantId: p.id, replace: job?.state === 'failed' ? job.id : null };
    input.click();
  }
  protected photoChosen(input: HTMLInputElement): void {
    const file = input.files?.[0],
      target = this.capture;
    input.value = '';
    this.capture = null;
    if (!file || !target) return;
    if (target.replace) this.actions.discardPhoto(target.replace);
    this.checkDay();
    void this.actions.takePhoto(target.plantId, file, new Date());
  }
  protected retry(job: WaterJob | PhotoJob): void {
    if ('phase' in job) void this.actions.retryPhoto(job.id);
    else void this.actions.retryWater(job.id);
  }
  protected isPhoto(job: WaterJob | PhotoJob): boolean {
    return 'phase' in job;
  }
  protected plantName(id: string): string {
    return this.plants().find((p) => p.id === id)?.name ?? 'Plant';
  }
  protected imageFailed(src: string): void {
    this.brokenImages.update((items) => new Set([...items, src]));
  }
  private isWatered(id: string): boolean {
    return (
      !!(this.historyKnown() && this.history()?.watered.has(id)) ||
      this.actions
        .waters()
        .some((j) => j.plantId === id && j.dayKey === this.dayKey() && j.state === 'saved')
    );
  }
  private readonly checkDay = (): void => {
    if (roundDay(new Date()).key !== this.dayKey()) void this.refreshHistory();
  };
  private message(error: unknown): string {
    return error instanceof Error ? error.message : 'Could not reach the server. Please try again.';
  }
}
