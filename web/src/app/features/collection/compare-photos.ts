import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  inject,
  signal,
} from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { UiPhotoViewer } from '../../ui/photo-viewer';
import { Subscription } from 'rxjs';
import { JournalDocument, type JournalQuery, type PhotoViewpoint } from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient } from '../../core/graphql/client';
import { dateSourceLabel, dayLabel } from '../journal/journal-view';

type PhotoRow = Extract<
  JournalQuery['journal']['items'][number],
  { __typename: 'PhotoEntry' }
>['photo'];

/** Honest viewpoint word for the mismatch label — never a score, never a
 * growth percentage: the frozen contract carries no growth metric and this
 * screen invents none. */
function viewpointWord(v: PhotoViewpoint): string {
  switch (v) {
    case 'WHOLE':
      return 'whole plant';
    case 'LEAF':
      return 'leaf';
    case 'FRUIT':
      return 'fruit';
    case 'OTHER':
      return 'other';
    default: {
      const _never: never = v;
      return _never;
    }
  }
}

/** The date a caption shows: the stated capture instant when there is one,
 * otherwise the upload instant — always labelled with its provenance. */
function captionDate(ph: PhotoRow): string {
  return dayLabel(ph.takenAt ?? ph.createdAt);
}

/** Growth comparison over a plant's photo series (T20). Deep-linkable:
 * /plants/:id/compare?a=<photoId>&b=<photoId>. A selection that no longer
 * resolves (deleted photo, foreign id) recovers to the picker with an honest
 * note rather than rendering a half-pair. No transitions anywhere: reduced
 * motion is honored by having none. */
@Component({
  selector: 'app-compare-photos',
  imports: [RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (state() === 'loading') {
      <p class="caption">Loading photos…</p>
    } @else if (loadError()) {
      <section role="alert">
        <h1>Photos could not be loaded</h1>
        <p>{{ loadError() }}</p>
        <button type="button" class="btn" (click)="loadSeries()">Retry photos</button>
      </section>
    } @else if (pair(); as pr) {
      <h1>Compare photos</h1>
      <p class="small" style="margin:0 0 var(--sp-2)">
        <a routerLink="/plants/{{ plantId }}" [queryParams]="{ tab: 'gallery' }">Back to gallery</a>
      </p>
      @if (pr.left.viewpoint !== pr.right.viewpoint) {
        <p class="small" style="color:var(--text-muted)">
          Viewpoints differ — this pair was shot from different angles ({{
            viewpointWord(pr.left.viewpoint)
          }}
          vs {{ viewpointWord(pr.right.viewpoint) }}). No growth percentage is invented.
        </p>
      } @else {
        <p class="small" style="color:var(--text-muted)">
          Same viewpoint — this pair was shot from the same angle. No growth percentage is invented.
        </p>
      }

      <div class="row" style="margin:var(--sp-4) 0">
        <button
          type="button"
          class="chip"
          [attr.aria-pressed]="mode() === 'side'"
          (click)="mode.set('side')"
        >
          Side by side
        </button>
        <button
          type="button"
          class="chip"
          [attr.aria-pressed]="mode() === 'overlay'"
          (click)="mode.set('overlay')"
        >
          Overlay slider
        </button>
        <button type="button" class="chip" [attr.aria-pressed]="crop()" (click)="crop.set(!crop())">
          Fill crop
        </button>
      </div>

      @if (mode() === 'side') {
        <div class="grid-cards" style="grid-template-columns:1fr 1fr">
          @for (p of [pr.left, pr.right]; track p.id) {
            <figure class="card" style="margin:0; padding:0; overflow:hidden">
              <button
                type="button"
                [attr.aria-label]="'View photo from ' + captionDate(p) + ' fullscreen'"
                (click)="openPhoto(p)"
                style="display:block;width:100%;padding:0;border:0;cursor:zoom-in;background:transparent"
              >
                <img
                  [src]="mediaUrl(p)"
                  [alt]="'Photo from ' + captionDate(p)"
                  [style.object-fit]="crop() ? 'cover' : 'contain'"
                  [style.background]="'var(--surface-canvas)'"
                  style="width:100%; aspect-ratio:4/3; display:block"
                />
              </button>
              <figcaption class="small" style="padding:var(--sp-3) var(--sp-4)">
                <strong class="num">{{ captionDate(p) }}</strong> —
                {{ dateSourceLabel(p.dateSource) }}
                @if (p.caption) {
                  <span> · {{ p.caption }}</span>
                }
              </figcaption>
            </figure>
          }
        </div>
      } @else {
        <div class="card" style="padding:0; overflow:hidden; position:relative">
          <div style="position:relative; aspect-ratio:4/3">
            <img
              [src]="mediaUrl(pr.left)"
              [alt]="'Left photo from ' + captionDate(pr.left)"
              style="position:absolute; inset:0; width:100%; height:100%; object-fit:contain; background:var(--surface-canvas)"
            />
            <img
              [src]="mediaUrl(pr.right)"
              [alt]="'Right photo from ' + captionDate(pr.right)"
              [style.clip-path]="'inset(0 0 0 ' + split() + '%)'"
              style="position:absolute; inset:0; width:100%; height:100%; object-fit:contain"
            />
            <div
              [style.left.%]="split()"
              style="position:absolute; top:0; bottom:0; width:2px; background:var(--accent); transform:translateX(-1px)"
            ></div>
          </div>
          <div style="padding:var(--sp-3) var(--sp-4)">
            <label class="field-label" for="split">Overlay position</label>
            <input
              id="split"
              type="range"
              min="0"
              max="100"
              step="1"
              class="input"
              [value]="split()"
              (input)="split.set(+$any($event.target).value)"
              aria-label="Reveal right photo percentage"
            />
          </div>
        </div>
        <p class="caption">Arrow keys move the handle; Home/End jump to either photo.</p>
      }

      <div class="row" style="margin-top:var(--sp-4)">
        <button type="button" class="btn" (click)="swap()">Swap left/right</button>
        <button type="button" class="btn" (click)="changeSelection()">Change selection</button>
      </div>
    } @else {
      <h1>Compare photos</h1>
      @if (lostIds().length) {
        <section class="card" role="alert" style="max-width:640px; margin-bottom:var(--sp-4)">
          <p class="caption" style="margin:0">
            A selected photo is no longer available — it was removed or never existed here. Pick a
            new pair below.
          </p>
        </section>
      }
      @if (series().length < 2) {
        <h2>Pick two photos</h2>
        <p class="caption">
          This plant has {{ series().length }} photo{{ series().length === 1 ? '' : 's' }}. A
          comparison needs two — add another from the plant's Gallery tab.
        </p>
      } @else {
        <h2>Pick two photos</h2>
        <p class="caption">Choose a left and a right photo to compare.</p>
        <ul style="list-style:none; margin:var(--sp-4) 0; padding:0">
          @for (ph of series(); track ph.id) {
            <li
              class="row wrap"
              style="gap:var(--sp-3); align-items:center; border-top:1px solid var(--border-decorative); padding:var(--sp-2) 0"
            >
              <img
                [src]="mediaUrl(ph)"
                [alt]="'Photo from ' + captionDate(ph)"
                style="width:96px; aspect-ratio:4/3; object-fit:cover; display:block; background:var(--surface-canvas)"
              />
              <span class="small">
                <strong class="num">{{ captionDate(ph) }}</strong> —
                {{ dateSourceLabel(ph.dateSource) }} · {{ viewpointWord(ph.viewpoint) }}
              </span>
              <span class="row" style="gap:var(--sp-2)">
                <button type="button" class="btn" (click)="pick('a', ph.id)">Use as left</button>
                <button type="button" class="btn" (click)="pick('b', ph.id)">Use as right</button>
              </span>
            </li>
          }
        </ul>
      }
    }
  `,
})
export class ComparePhotos implements OnInit, OnDestroy {
  private readonly viewer = inject(UiPhotoViewer);
  private readonly abort = new AbortController();
  private closeViewer?: () => void;
  private readonly gql = inject(GraphQLClient);
  private readonly session = inject(SessionService);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly subs = new Subscription();

  protected readonly state = signal<'loading' | 'ready'>('loading');
  protected readonly loadError = signal('');
  protected readonly series = signal<PhotoRow[]>([]);
  protected readonly mode = signal<'side' | 'overlay'>('side');
  protected readonly crop = signal(false);
  protected readonly split = signal(50);
  private readonly selA = signal<string | null>(null);
  private readonly selB = signal<string | null>(null);

  protected readonly plantId = this.route.snapshot.paramMap.get('id') ?? '';

  /** Params that were supplied but do not resolve in this plant's series —
   * the deleted-selection case. Only honest: never silently drop one side. */
  protected readonly lostIds = computed(() => {
    if (this.state() !== 'ready') return [];
    const ids = new Set(this.series().map((p) => p.id));
    return [this.selA(), this.selB()].filter((id) => id !== null && !ids.has(id));
  });

  protected readonly pair = computed(() => {
    if (this.state() !== 'ready') return null;
    const a = this.selA();
    const b = this.selB();
    if (!a || !b || a === b) return null; // one photo compared with itself is not a pair
    const left = this.series().find((p) => p.id === a);
    const right = this.series().find((p) => p.id === b);
    return left && right ? { left, right } : null;
  });

  async ngOnInit(): Promise<void> {
    this.subs.add(
      this.route.queryParamMap.subscribe((pm) => {
        this.selA.set(pm.get('a'));
        this.selB.set(pm.get('b'));
      }),
    );
    await this.loadSeries();
  }
  protected async loadSeries(): Promise<void> {
    this.state.set('loading');
    this.loadError.set('');
    const photos: PhotoRow[] = [];
    let after: string | undefined;
    const seen = new Set<string>();
    do {
      const res = await this.gql.query(
        JournalDocument,
        {
          plantId: this.plantId,
          first: 100,
          after,
          filter: { entryTypes: ['PHOTO'] },
        },
        this.abort.signal,
      );
      if (this.abort.signal.aborted) return;
      if (res.kind === 'auth') {
        this.session.markExpired();
        return;
      }
      if (res.kind !== 'ok' || !res.data) {
        this.loadError.set('Could not reach the server. Your photos have not changed.');
        this.state.set('ready');
        return;
      }
      photos.push(
        ...res.data.journal.items.flatMap((e) => (e.__typename === 'PhotoEntry' ? [e.photo] : [])),
      );
      if (!res.data.journal.pageInfo.hasNextPage) break;
      const cursor = res.data.journal.pageInfo.endCursor;
      if (!cursor || seen.has(cursor)) {
        this.loadError.set('The remaining photos could not be loaded. Try again.');
        this.state.set('ready');
        return;
      }
      seen.add(cursor);
      after = cursor;
    } while (!this.abort.signal.aborted);
    this.series.set(photos);
    this.state.set('ready');
  }

  ngOnDestroy(): void {
    this.subs.unsubscribe();
    this.abort.abort();
    this.closeViewer?.();
  }

  protected mediaUrl(ph: PhotoRow): string {
    return `/api/media/${ph.asset.id}/display`;
  }

  protected openPhoto(photo: PhotoRow): void {
    this.closeViewer = this.viewer.open(
      this.series().map((p) => ({
        id: p.id,
        src: this.mediaUrl(p),
        original: `/api/media/${p.asset.id}/original`,
        thumbnail: `/api/media/${p.asset.id}/thumb`,
        alt: p.caption || 'Plant photo',
        detail: captionDate(p) + ' · ' + dateSourceLabel(p.dateSource),
      })),
      this.series().findIndex((p) => p.id === photo.id),
    );
  }

  protected pick(slot: 'a' | 'b', id: string): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { [slot]: id },
      queryParamsHandling: 'merge',
    });
  }

  protected swap(): void {
    const a = this.selA();
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { a: this.selB(), b: a },
      queryParamsHandling: 'merge',
    });
  }

  protected changeSelection(): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { a: null, b: null },
      queryParamsHandling: 'merge',
    });
  }

  protected readonly dateSourceLabel = dateSourceLabel;
  protected readonly viewpointWord = viewpointWord;
  protected readonly captionDate = captionDate;
}
