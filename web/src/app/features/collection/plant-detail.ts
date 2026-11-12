import {
  ChangeDetectionStrategy,
  DestroyRef,
  Component,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { ActivatedRoute, Router, RouterLink, RouterOutlet } from '@angular/router';
import {
  CreateLocationDocument,
  DeletePhotoDocument,
  JournalDocument,
  LocationsDocument,
  PlantDetailDocument,
  SetPlantContextDocument,
  SetPlantTagsDocument,
  TagsDocument,
  UpdatePhotoDocument,
  type ContextPlacement,
  type JournalQuery,
  type LocationSituation,
  type PlantFieldsFragment,
} from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { CareTab } from '../care/care-tab';
import { InsightsTab } from '../insights/insights-tab';
import { QuickLog } from '../journal/quick-log';
import { PhotoQueue } from '../capture/photo-queue';
import { JournalTimeline } from '../journal/journal-timeline';
import { dateSourceLabel } from '../journal/journal-view';
import { UiIcon } from '../../ui/icon';
import { UiPhotoViewer, type ViewerPhoto } from '../../ui/photo-viewer';
import { UiPhoto } from '../../ui/photo';
import { UiTab, UiTabs } from '../../ui/tabs';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiToasts } from '../../ui/toast';
import {
  blankToNull,
  conflictNote,
  generalProblems,
  parseDecimal,
  type PayloadProblem,
} from './shared';

const PLACEMENTS: ContextPlacement[] = ['GROUND', 'CONTAINER'];
const SITUATIONS: LocationSituation[] = ['INDOOR', 'COVERED', 'OUTDOOR'];

/** Deep-linkable sections; anything else in `?tab=` falls back to journey. */
const TAB_VALUES = ['journey', 'gallery', 'care', 'insights', 'details'] as const;

/** A plant's photo, read off the frozen journal union's PHOTO member —
 * the only contract-side list of a plant's photos. */
type PhotoRow = Extract<
  JournalQuery['journal']['items'][number],
  { __typename: 'PhotoEntry' }
>['photo'];

/**
 * Plant detail (T10, restyled by UI01 pass C): photo hero carrying the
 * identity facts, Journey/Care/Insights/Details tabs, and inside Details the photo
 * gallery, a current-context card with a move form (context changes are
 * history, not edits), provenance, and tag editing. Every mutation is
 * revision-guarded; conflicts explain what happened and reload the current
 * state instead of silently overwriting.
 *
 * Provenance honesty: the acquisition fragment carries pricingState but no
 * currency and no purchase pointer, so allocation minor strings are never
 * formatted as a guessed currency — we say "recorded" and point at the
 * purchases page, which holds the real money.
 */
@Component({
  selector: 'plant-detail',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    RouterLink,
    RouterOutlet,
    UiTabs,
    UiTab,
    UiPhoto,
    UiIcon,
    UiEntryDialog,
    JournalTimeline,
    PhotoQueue,
    QuickLog,
    CareTab,
    InsightsTab,
  ],
  templateUrl: './plant-detail.html',
  styleUrls: ['./plant-detail.css', './plant-gallery.css'],
})
export class PlantDetail {
  private readonly viewer = inject(UiPhotoViewer);
  private photoRequest?: AbortController;
  private closeViewer?: () => void;
  protected readonly photoLoading = signal(false);
  protected readonly photoError = signal('');
  protected readonly compareSelection = signal<string[]>([]);
  protected readonly choosingComparison = signal(false);
  protected readonly photoOptions = signal<PhotoRow | null>(null);
  private readonly gql = inject(GraphQLClient);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly toasts = inject(UiToasts);
  private readonly session = inject(SessionService);

  protected readonly placements = PLACEMENTS;
  protected readonly situations = SITUATIONS;

  protected readonly state = signal<'loading' | 'ready' | 'missing'>('loading');
  protected readonly plant = signal<PlantFieldsFragment | null>(null);
  protected readonly photos = signal<PhotoRow[]>([]);
  protected readonly confirmPhoto = signal<string | null>(null);
  protected readonly banner = signal('');
  protected readonly tab = signal<(typeof TAB_VALUES)[number]>('journey');
  protected readonly busy = signal(false);
  protected readonly moving = signal(false);
  protected readonly tagEditing = signal(false);
  /** The photo modal is kept mounted across background plant re-reads, so a
   * live queue is never destroyed mid-upload. Only the very first load, when
   * there is nothing on screen yet, may swap in the skeleton. */
  protected readonly photosDialogOpen = signal(false);
  protected readonly waterDialogOpen = signal(false);
  protected readonly waterBusy = signal(false);
  private readonly journey = viewChild(JournalTimeline);

  protected onWaterSaved(): void {
    this.waterDialogOpen.set(false);
    this.waterBusy.set(false);
    this.journey()?.refresh();
  }

  protected readonly locations = signal<
    { id: string; name: string; parent: { name: string } | null }[]
  >([]);
  protected readonly availableTags = signal<{ id: string; label: string }[]>([]);
  protected readonly tagDraft = signal<string[]>([]);

  /** The queue lives inside the photo modal and only exists once that modal
   * has rendered; its own state drives the dialog's busy/dirty. */
  private readonly queue = viewChild(PhotoQueue);
  protected readonly queueBusy = computed(() => this.queue()?.busy() ?? false);
  protected readonly queueDirty = computed(() => this.queue()?.dirty() ?? false);

  /** Recorded tag ids, for the read-only list on the page. */
  protected readonly tagIds = computed(() => new Set((this.plant()?.tags ?? []).map((t) => t.id)));

  /** The entry dialogs are declarative: each one's `[open]` binds straight to
   * its own state flag, so no alias computeds sit between the flag and the
   * dialog. Cancels go through the dialog handle by template ref. */
  private readonly moveEntry = viewChild<UiEntryDialog>('moveEntry');
  private readonly tagEntry = viewChild<UiEntryDialog>('tagEntry');

  protected tagDirty(): boolean {
    const cur = [...(this.plant()?.tags ?? []).map((t) => t.id)].sort();
    const draft = [...this.tagDraft()].sort();
    return cur.length !== draft.length || cur.some((id, i) => id !== draft[i]);
  }

  /** True once the plant has rendered at least once. Background refreshes
   * must not blank the page — the photo queue keeps in-flight state and
   * un-attached rows inside the rendered subtree. */
  private havePlant = false;

  /** ?dialog=photos is consumed once: later query-param churn (e.g. a tab
   * switch) must never re-open or silently close a dirty modal. */
  private dialogIntentConsumed = false;

  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.photoRequest?.abort();
      this.closeViewer?.();
    });
    this.route.paramMap.subscribe((pm) => {
      const id = pm.get('id');
      if (id) void this.load(id);
    });
    // ?tab= deep link (UI01 pass C): lets other pages send someone straight
    // to the details section — e.g. Today's "Add a photo" for a plant with
    // no photo. A missing or unknown value resets to the default tab, so a
    // back navigation can never leave a stale Details panel on screen.
    // ?dialog=photos (UI02) is the same invitation, but it opens the shared
    // Add-photos modal instead of just moving the tab.
    this.route.queryParamMap.subscribe((qm) => {
      const tab = qm.get('tab');
      if ((TAB_VALUES as readonly string[]).includes(tab ?? '')) {
        this.tab.set(tab as (typeof TAB_VALUES)[number]);
      } else {
        this.tab.set('journey');
      }
      if (!this.dialogIntentConsumed && qm.get('dialog') === 'photos') {
        this.dialogIntentConsumed = true;
        this.openPhotoDialog();
      }
    });
  }

  protected locationPath(c: NonNullable<PlantFieldsFragment['currentContext']>): string {
    const loc = c.location;
    if (!loc) return 'Location not set';
    return loc.parent ? `${loc.parent.name} › ${loc.name}` : loc.name;
  }

  /**
   * Acquisition date as the contract stores it: LocalDate is already
   * 'YYYY-MM-DD', so no DatePipe — formatting a date-only value through a
   * Date would move it a day at the UTC boundary.
   */
  protected acquiredOn(p: PlantFieldsFragment): string | null {
    return p.acquisition?.acquiredOn ?? p.acquiredOn;
  }

  /** Pricing state only: the acquisition fragment carries no currency, so the
   * hero says "Recorded" and links to the page that holds the real numbers. */
  protected priceKnown(p: PlantFieldsFragment): boolean {
    return p.acquisition?.pricingState === 'KNOWN';
  }

  private async load(id: string): Promise<void> {
    // Skeleton only on first load: background refreshes (photo attach/remove,
    // cover change, conflict reload) must not destroy the rendered subtree —
    // the upload queue keeps in-flight state inside it.
    const first = !this.plant();
    if (first) this.state.set('loading');
    const [res, loc, tags] = await Promise.all([
      this.gql.query(PlantDetailDocument, { id }),
      this.gql.query(LocationsDocument),
      this.gql.query(TagsDocument, { first: 100 }),
    ]);
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (loc.kind === 'ok' && loc.data) this.locations.set(loc.data.locations);
    if (tags.kind === 'ok' && tags.data)
      this.availableTags.set(tags.data.tags.items.filter((t) => !t.archivedAt));
    if (res.kind !== 'ok' || !res.data) {
      if (first) this.state.set('missing');
      this.banner.set('Could not reach the server — showing nothing rather than guessing.');
      return;
    }
    const p = res.data.plant;
    if (!p) {
      if (first) this.state.set('missing');
      return;
    }
    if (this.plant()?.id !== p.id) {
      this.photos.set([]);
      this.compareSelection.set([]);
    }
    this.plant.set(p);
    this.havePlant = true;
    // The page's tag list is the saved record; the modal's draft follows it
    // on every re-read, so a background refresh never leaves stale checks.
    if (!this.tagEditing()) this.tagDraft.set(p.tags.map((t) => t.id));
    this.state.set('ready');
    void this.loadPhotos(p.id);
  }

  protected reload(): void {
    this.banner.set('');
    const id = this.plant()?.id ?? this.route.snapshot.paramMap.get('id');
    if (id) void this.load(id);
  }

  /** The edit form modal renders in this page's outlet (nested in a later
   * pass). When it deactivates, re-read the plant so saved edits show
   * immediately. */
  protected onOutletDeactivate(): void {
    const id = this.plant()?.id;
    if (id) void this.load(id);
  }

  protected mediaUrl(assetId: string, variant: 'thumb' | 'display' | 'original'): string {
    return `/api/media/${assetId}/${variant}`;
  }

  /** Hero image: the plant's own cover, display variant; null = no photo. */
  protected coverUrl(p: PlantFieldsFragment): string | null {
    return p.coverPhoto ? this.mediaUrl(p.coverPhoto.asset.id, 'display') : null;
  }

  protected readonly dateSourceLabel = dateSourceLabel;

  /** Tab switch rewrites ?tab= so the section survives a refresh or a share. */
  protected onTabSelect(value: string): void {
    this.tab.set(value as (typeof TAB_VALUES)[number]);
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { tab: value },
      queryParamsHandling: 'merge',
    });
  }

  /** Header "Add photo" and the Photos-section "Add photos" both open the
   * same shared modal. Nothing here touches the file input: choosing photos
   * stays an explicit action inside the queue. */
  protected openPhotoDialog(): void {
    this.photosDialogOpen.set(true);
  }

  protected onPhotosDismissed(): void {
    this.photosDialogOpen.set(false);
  }

  protected async loadPhotos(plantId: string): Promise<void> {
    this.photoRequest?.abort();
    const controller = new AbortController();
    this.photoRequest = controller;
    this.photoLoading.set(true);
    this.photoError.set('');
    const photos: PhotoRow[] = [];
    const cursors = new Set<string>();
    let after: string | undefined;
    try {
      do {
        const res = await this.gql.query(
          JournalDocument,
          { plantId, first: 100, after, filter: { entryTypes: ['PHOTO'] } },
          controller.signal,
        );
        if (controller.signal.aborted) return;
        if (res.kind === 'auth') {
          this.session.markExpired();
          return;
        }
        if (res.kind !== 'ok' || !res.data) throw Error('Photos could not be loaded. Try again.');
        const page = res.data.journal;
        photos.push(...page.items.flatMap((e) => (e.__typename === 'PhotoEntry' ? [e.photo] : [])));
        if (!page.pageInfo.hasNextPage) break;
        const cursor = page.pageInfo.endCursor;
        if (!cursor || cursors.has(cursor))
          throw Error('The remaining photos could not be loaded. Try again.');
        cursors.add(cursor);
        after = cursor;
      } while (!controller.signal.aborted);
      if (this.plant()?.id !== plantId) return;
      this.photos.set(
        [...new Map(photos.map((p) => [p.id, p])).values()].sort((a, b) => {
          const left = a.dateSource === 'UPLOAD_FALLBACK' ? null : a.takenAt;
          const right = b.dateSource === 'UPLOAD_FALLBACK' ? null : b.takenAt;
          return left && right
            ? right.localeCompare(left)
            : left
              ? -1
              : right
                ? 1
                : b.createdAt.localeCompare(a.createdAt);
        }),
      );
      this.compareSelection.update((ids) => ids.filter((id) => photos.some((p) => p.id === id)));
    } catch (error) {
      if (!controller.signal.aborted)
        this.photoError.set(error instanceof Error ? error.message : 'Photos could not be loaded.');
    } finally {
      if (!controller.signal.aborted) this.photoLoading.set(false);
    }
  }

  protected photoDate(photo: PhotoRow): string {
    if (photo.dateSource === 'UPLOAD_FALLBACK')
      return 'Capture date unknown · Uploaded ' + photo.createdAt.slice(0, 10) + ' UTC';
    return photo.takenAt
      ? photo.takenAt.slice(0, 10) + ' UTC · ' + dateSourceLabel(photo.dateSource)
      : 'Capture date unknown';
  }
  protected openPhoto(photo: PhotoRow, forceView = false): void {
    if (this.choosingComparison() && !forceView) {
      this.compareSelection.update((ids) =>
        ids.includes(photo.id)
          ? ids.filter((id) => id !== photo.id)
          : ids.length < 2
            ? [...ids, photo.id]
            : [ids[1], photo.id],
      );
      return;
    }
    const items: ViewerPhoto[] = this.photos().map((p) => ({
      id: p.id,
      src: this.mediaUrl(p.asset.id, 'display'),
      original: this.mediaUrl(p.asset.id, 'original'),
      thumbnail: this.mediaUrl(p.asset.id, 'thumb'),
      alt: p.caption || 'Photo of ' + this.plant()?.name,
      detail: this.photoDate(p),
      details: () => {
        this.closeViewer?.();
        this.photoOptions.set(p);
      },
    }));
    this.closeViewer = this.viewer.open(
      items,
      items.findIndex((p) => p.id === photo.id),
    );
  }
  protected comparePhotos(): void {
    const [a, b] = this.compareSelection();
    if (a && b)
      void this.router.navigate(['/plants', this.plant()?.id, 'compare'], {
        queryParams: { a, b },
      });
  }

  protected onPhotosChanged(): void {
    const id = this.plant()?.id;
    if (id) {
      void this.loadPhotos(id);
      void this.load(id); // photoCount and the cover tile follow the attach
    }
  }

  protected async makeCover(ph: PhotoRow): Promise<void> {
    const p = this.plant();
    if (!p || this.busy()) return;
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(UpdatePhotoDocument, {
        input: { id: ph.id, expectedRevision: ph.revision, isCover: true },
      });
      if (!(await this.handle(res, (d) => d.updatePhoto, 'Cover updated', p.id))) return;
      void this.loadPhotos(p.id); // the cover badge moves without a reload
    } finally {
      this.busy.set(false);
    }
  }

  protected async removePhoto(): Promise<void> {
    const p = this.plant();
    const id = this.confirmPhoto();
    if (!p || !id || this.busy()) return;
    const ph = this.photos().find((x) => x.id === id);
    if (!ph) {
      this.confirmPhoto.set(null);
      return;
    }
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(DeletePhotoDocument, {
        input: { id: ph.id, expectedRevision: ph.revision },
      });
      // On success the confirmation closes; on failure/conflict it stays
      // mounted with the honest note so the retry uses the fresh revision
      // the reload just pulled in.
      if (await this.handle(res, (d) => d.deletePhoto, 'Photo removed', p.id)) {
        this.confirmPhoto.set(null);
        void this.loadPhotos(p.id); // the row disappears without a reload
      }
    } finally {
      this.busy.set(false);
    }
  }

  protected toggleTag(id: string): void {
    this.tagDraft.update((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]));
  }

  protected resetTags(): void {
    this.tagDraft.set((this.plant()?.tags ?? []).map((t) => t.id));
  }

  protected async saveTags(): Promise<void> {
    const p = this.plant();
    if (!p || this.busy()) return;
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(SetPlantTagsDocument, {
        input: {
          plantId: p.id,
          expectedRevision: p.revision,
          tagIds: this.tagDraft(),
        },
      });
      // Success closes the modal (the shared handle's finish does it); a
      // failure or conflict keeps it open with the draft intact so the save
      // can retry against the reloaded revision.
      if (await this.handle(res, (d) => d.setPlantTags, 'Tags saved', p.id)) {
        this.tagEditing.set(false);
      }
    } finally {
      this.busy.set(false);
    }
  }

  protected startTagEdit(): void {
    // The draft always starts from the saved record, never from a stale draft.
    this.tagDraft.set((this.plant()?.tags ?? []).map((t) => t.id));
    this.tagEditing.set(true);
  }

  /** Discard goes through the shared handle, so the same confirmation guards
   * it as an X; the draft only resets once the dismissal lands. */
  protected cancelTagEdit(): void {
    this.tagEntry()?.requestClose();
  }

  protected onTagsDismissed(): void {
    this.tagEditing.set(false);
    this.resetTags();
  }

  protected startMove(): void {
    this.moving.set(true);
  }

  /** Cancel goes through the shared handle, so a dirty form gets the same
   * confirmation as any other exit; the form stays until the dismissal lands. */
  protected cancelMove(): void {
    this.moveEntry()?.requestClose();
  }

  protected onMoveDismissed(): void {
    this.moving.set(false);
  }

  protected async move(el: HTMLFormElement): Promise<void> {
    const p = this.plant();
    if (!p || this.busy()) return;
    const data = Object.fromEntries(new FormData(el));
    const pot = parseDecimal(String(data['potVolumeLiters'] ?? ''));
    if (pot.error) {
      this.banner.set(pot.error ?? 'Invalid pot volume.');
      return;
    }
    this.busy.set(true);
    try {
      let locationId = blankToNull(String(data['locationId'] ?? ''));
      const newLoc = blankToNull(String(data['newLocation'] ?? ''));
      if (newLoc) {
        const res = await this.gql.mutate(CreateLocationDocument, {
          input: {
            name: newLoc,
            situation: (data['newLocationSituation'] as LocationSituation) ?? 'INDOOR',
          },
        });
        if (res.kind === 'auth') {
          this.session.markExpired();
          return;
        }
        const problems = (res.kind === 'ok' ? res.data?.createLocation?.problems : []) ?? [];
        if (problems.length > 0) {
          this.banner.set(`New location: ${problems[0].message}`);
          return;
        }
        locationId =
          res.kind === 'ok' ? (res.data?.createLocation?.location?.id ?? locationId) : locationId;
      }
      const res = await this.gql.mutate(SetPlantContextDocument, {
        input: {
          plantId: p.id,
          expectedRevision: p.revision,
          context: {
            placement: (data['placement'] as ContextPlacement) ?? 'CONTAINER',
            locationId,
            potVolumeLiters: pot.value,
          },
        },
      });
      if (!(await this.handle(res, (d) => d.setPlantContext, 'Move recorded', p.id))) return;
      this.moving.set(false);
    } finally {
      this.busy.set(false);
    }
  }

  /** Shared mutation outcome: true = success (already reloaded). */
  private async handle<T>(
    res: GqlResult<T>,
    pick: (data: T) => { problems?: PayloadProblem[] } | null | undefined,
    okLabel: string,
    plantId: string,
  ): Promise<boolean> {
    if (res.kind === 'auth') {
      this.session.markExpired();
      return false;
    }
    if (res.kind !== 'ok') {
      this.banner.set('Could not reach the server. Nothing was changed.');
      return false;
    }
    const problems = (res.data ? pick(res.data)?.problems : []) ?? [];
    if (problems.length > 0) {
      const note = conflictNote(problems, 'This plant');
      this.banner.set(note || generalProblems(problems).join(' ') || problems[0].message);
      void this.load(plantId); // actionable: show the current state
      return false;
    }
    this.toasts.ok(okLabel);
    void this.load(plantId);
    return true;
  }
}
