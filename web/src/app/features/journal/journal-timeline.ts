import { NgTemplateOutlet } from '@angular/common';
import { requestId } from '../../core/request-id';
import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  effect,
  inject,
  input,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import {
  DeleteMeasurementDocument,
  JournalDocument,
  VoidActivityDocument,
  type JournalEntryType,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { PageStore, type Page } from '../../core/graphql/page-store';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon, type UiIconName } from '../../ui/icon';
import { UiPhoto } from '../../ui/photo';
import { UiSkeleton } from '../../ui/skeleton';
import { UiToasts } from '../../ui/toast';
import { conflictNote, type PayloadProblem } from '../collection/shared';
import { QuickLog } from './quick-log';
import {
  dateSourceLabel,
  dayLabel,
  editableEntry,
  entryTypeLabel,
  groupByMonth,
  timeLabel,
  type ActivityEntry,
  type JournalItem,
  type MeasurementEntry,
} from './journal-view';

const FILTERS: { value: JournalEntryType | null; label: string }[] = [
  { value: null, label: 'All' },
  { value: 'ACTIVITY', label: 'Activities' },
  { value: 'MEASUREMENT', label: 'Measurements' },
  { value: 'PHOTO', label: 'Photos' },
  { value: 'PURCHASE', label: 'Purchases' },
  { value: 'CARE', label: 'Care' },
  { value: 'INSIGHT', label: 'Insights' },
];

/**
 * Journey timeline (T19, restyled by UI01 pass C, entry flows modalised by
 * UI02): chronological month groups over the frozen Journal union — editorial
 * month heads, a date rail, typed cards per __typename, PhotoEntry cards carry
 * the event's own photo (never the plant's cover). Owner entry/correction lives
 * in the shared UiEntryDialog: the same QuickLog component is projected into a
 * modal (no inline expanding panel), and void/delete use the same compact
 * guarded dialog with their existing two-step explicit action/cancel semantics.
 * Keyset load-more with entry ids as track keys so a refresh after any
 * mutation can never duplicate a card.
 * Failure renders an explicit error card, never a fake empty state.
 */
@Component({
  selector: 'journal-timeline',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [NgTemplateOutlet, RouterLink, UiEntryDialog, UiIcon, UiPhoto, UiSkeleton, QuickLog],
  templateUrl: './journal-timeline.html',
  styleUrl: './journal-timeline.css',
})
export class JournalTimeline implements OnInit, OnDestroy {
  /** Plant-scoped when set; null = whole-workspace timeline. */
  readonly plantId = input<string | null>(null);

  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  private readonly abort = new AbortController();

  protected readonly filters = FILTERS;
  protected readonly activeType = signal<JournalEntryType | null>(null);
  protected readonly filtersOpen = signal(false);
  protected readonly from = signal('');
  protected readonly until = signal('');
  protected readonly includeVoided = signal(false);
  protected readonly draftType = signal<JournalEntryType | null>(null);
  protected readonly draftFrom = signal('');
  protected readonly draftUntil = signal('');
  protected readonly draftVoided = signal(false);
  protected readonly filterError = signal('');
  protected readonly filterCount = computed(
    () =>
      Number(!!this.activeType()) +
      Number(!!this.from() || !!this.until()) +
      Number(this.includeVoided()),
  );
  protected readonly filterLabel = computed(() =>
    [
      this.filters.find((f) => f.value === this.activeType())?.label || 'All',
      this.from() || this.until() ? 'Date range' : '',
      this.includeVoided() ? 'Including voided' : '',
    ]
      .filter(Boolean)
      .join(' · '),
  );
  protected openFilters(): void {
    this.draftType.set(this.activeType());
    this.draftFrom.set(this.from());
    this.draftUntil.set(this.until());
    this.draftVoided.set(this.includeVoided());
    this.filterError.set('');
    this.filtersOpen.set(true);
  }
  protected resetFilterDraft(): void {
    this.draftType.set(null);
    this.draftFrom.set('');
    this.draftUntil.set('');
    this.draftVoided.set(false);
    this.filterError.set('');
  }
  protected saveFilters(): void {
    const from = this.draftFrom(),
      to = this.draftUntil();
    if (
      (from && !Number.isFinite(new Date(from).getTime())) ||
      (to && !Number.isFinite(new Date(to).getTime())) ||
      (from && to && new Date(from) > new Date(to))
    ) {
      this.filterError.set('Choose a valid range with the end after the start.');
      return;
    }
    this.from.set(from);
    this.until.set(to);
    this.includeVoided.set(this.draftVoided());
    this.filtersOpen.set(false);
    this.applyFilter(this.draftType());
  }
  protected clearAllFilters(): void {
    this.from.set('');
    this.until.set('');
    this.includeVoided.set(false);
    this.applyFilter(null);
  }
  protected readonly editing = signal<JournalItem | null>(null);
  /** the quick-log/edit modal: one entry dialog for both flows */
  protected readonly entryOpen = signal(false);
  protected readonly entryBusy = signal(false);
  /** QuickLog resolved a save (or explained a conflict) — close the modal. */
  private readonly pendingSave = signal(false);
  private readonly pendingConflict = signal(false);
  /** Void/delete confirmation modal: the entry awaiting an explicit action. */
  protected readonly confirmEntry = signal<JournalItem | null>(null);
  protected readonly confirmBusy = signal(false);
  protected readonly voiding = signal<string | null>(null);
  protected readonly deleting = signal<string | null>(null);
  protected readonly hasNext = signal(false);

  protected readonly store: PageStore<JournalItem>;

  /** The quick-log/edit modal, so the host can drive its shared guard. */
  private readonly entry = viewChild<UiEntryDialog>('entry');

  /** QuickLog resolved a save (or an explained conflict) — the host closes the
   * modal through the shared component: markPristine() first, so a completed
   * save is never answered with "discard changes?". The refresh settles it,
   * so a failed reload still closes a form whose save did land. */
  private readonly closeResolvedEntry = effect(() => {
    const saving = this.pendingSave();
    const conflict = this.pendingConflict();
    if (!saving && !conflict) return;
    const loading = this.store.loading();
    if (loading) return;
    const entry = this.entry();
    if (!entry || !this.entryOpen()) return;
    untracked(() => {
      entry.markPristine();
      this.pendingSave.set(false);
      this.pendingConflict.set(false);
      this.entryOpen.set(false);
    });
  });

  constructor() {
    this.store = new PageStore((after) => this.fetchPage(after));
  }

  // plantId is only bound after the constructor — the first page loads here.
  ngOnInit(): void {
    void this.store.refresh();
  }

  ngOnDestroy(): void {
    this.abort.abort();
    this.store.destroy();
  }

  protected readonly groups = computed(() => groupByMonth(this.store.items()));

  protected active(f: JournalEntryType | null): boolean {
    return this.activeType() === f;
  }

  protected applyFilter(f: JournalEntryType | null): void {
    this.activeType.set(f);
    this.editing.set(null);
    this.entryOpen.set(false);
    this.entryBusy.set(false);
    this.pendingSave.set(false);
    this.pendingConflict.set(false);
    void this.store.refresh();
  }

  refresh(): void {
    void this.store.refresh();
  }

  protected retry(): void {
    void this.store.refresh();
  }

  protected startLog(): void {
    this.editing.set(null);
    this.entryBusy.set(false);
    this.pendingSave.set(false);
    this.pendingConflict.set(false);
    this.entryOpen.set(true);
  }

  protected startEdit(e: JournalItem): void {
    this.deleting.set(null);
    this.voiding.set(null);
    this.editing.set(e);
    this.entryBusy.set(false);
    this.pendingSave.set(false);
    this.pendingConflict.set(false);
    this.entryOpen.set(true);
  }

  /** Modal titles: the dialog header carries them, so QuickLog's own heading
   * is gone rather than duplicated. */
  protected entryTitle(): string {
    const e = this.editing();
    if (!e) return 'Add a moment';
    return e.__typename === 'MeasurementEntry' ? 'Edit measurement' : 'Edit activity';
  }

  protected entryDescription(): string {
    return this.editing()
      ? 'Correct the record — kind and unit stay frozen.'
      : 'Log an activity or a measurement. Backdating is explicit; blanks stay unrecorded.';
  }

  /** QuickLog's busy state drives the modal, so nothing closes mid-mutation. */
  protected onBusyChange(next: boolean): void {
    this.entryBusy.set(next);
    if (next) {
      this.pendingSave.set(false);
      this.pendingConflict.set(false);
    }
  }

  protected onCreated(): void {
    this.pendingSave.set(true);
    void this.store.refresh();
  }

  protected onEdited(): void {
    this.editing.set(null);
    this.pendingSave.set(true);
    void this.store.refresh();
  }

  /** A CONFLICT on an edit is explained by QuickLog and reloads the timeline;
   * the stale draft is dropped rather than kept open over refreshed truth. */
  protected onEditConflict(): void {
    this.editing.set(null);
    this.pendingConflict.set(true);
    void this.store.refresh();
  }

  /** Cancel/X/Escape/backdrop all go through the shared dirty guard; the
   * draft stays put unless the owner confirms the discard. */
  protected onEntryDismissed(): void {
    this.entryOpen.set(false);
    this.editing.set(null);
    this.entryBusy.set(false);
    this.pendingSave.set(false);
    this.pendingConflict.set(false);
  }

  /** QuickLog's Cancel: the shared guard decides, never a direct state clear. */
  protected onEntryCancelled(): void {
    this.entry()?.requestClose();
  }

  /** Detail line: notes and captions only — money never rides on cards, and
   * unknown optionals are absent rather than zero-filled. */
  protected detail(e: JournalItem): string {
    switch (e.__typename) {
      case 'ActivityEntry':
        return e.activity.note ?? '';
      case 'PhotoEntry':
        return e.photo.caption ?? '';
      case 'MeasurementEntry':
      case 'PurchaseEntry':
        return '';
      case 'CareEntry':
        return '';
      case 'InsightEntry':
        return '';
      default: {
        const _never: never = e;
        return _never;
      }
    }
  }

  protected methodLabel(m: string): string {
    return m === 'SENSOR' ? 'Sensor' : 'From photo';
  }

  /** Entry-kind glyph for the card's quiet kind marker (same icon language
   * as the shell). Exhaustive over the generated union. */
  protected kindIcon(e: JournalItem): UiIconName {
    switch (e.__typename) {
      case 'ActivityEntry':
        return 'drop';
      case 'MeasurementEntry':
        return 'chevrons';
      case 'PhotoEntry':
        return 'camera';
      case 'PurchaseEntry':
        return 'receipt';
      case 'CareEntry':
        return 'sprout';
      case 'InsightEntry':
        return 'sparkles';
      default: {
        const _never: never = e;
        return _never;
      }
    }
  }

  /** The event's own photo at a readable size — never the plant's cover. */
  protected photoUrl(e: JournalItem): string | null {
    if (e.__typename !== 'PhotoEntry') return null;
    return `/api/media/${e.photo.asset.id}/medium`;
  }

  /** Date rail: big day number, short month, exact UTC time below. */
  protected dayNumber(iso: string): string {
    return new Date(iso).toLocaleDateString('en-US', {
      day: 'numeric',
      timeZone: 'UTC',
    });
  }

  protected monthShort(iso: string): string {
    return new Date(iso).toLocaleDateString('en-US', {
      month: 'short',
      year: 'numeric',
      timeZone: 'UTC',
    });
  }

  protected dateSourceLabel = dateSourceLabel;
  protected editableEntry = editableEntry;
  protected entryTypeLabel = entryTypeLabel;
  protected dayLabel = dayLabel;
  protected timeLabel = timeLabel;

  /** Void/delete open the shared compact confirmation instead of expanding a
   * row. Nothing mutates until the explicit action button is pressed. */
  protected askVoid(e: JournalItem): void {
    if (e.__typename !== 'ActivityEntry') return;
    this.deleting.set(null);
    this.voiding.set(e.id);
    this.confirmEntry.set(e);
  }

  protected askDelete(e: JournalItem): void {
    if (e.__typename !== 'MeasurementEntry') return;
    this.voiding.set(null);
    this.deleting.set(e.id);
    this.confirmEntry.set(e);
  }

  protected confirmTitle(): string {
    return this.voiding() ? 'Void this activity?' : 'Delete this measurement?';
  }

  protected confirmBody(): string {
    return this.voiding()
      ? 'Voiding keeps the record as history. Nothing is deleted.'
      : 'Deleting removes this measurement. This cannot be undone from the timeline.';
  }

  protected confirmDescription(): string {
    const e = this.confirmEntry();
    const what = e ? e.summary : '';
    return this.voiding()
      ? `Voiding keeps "${what}" in the timeline as history — it is not deleted.`
      : `Deleting removes "${what}" from this plant’s measurements.`;
  }

  /** Cancel/X/Escape/backdrop on the confirmation: clears the two-step state,
   * never the mutation. */
  protected onConfirmDismissed(): void {
    this.confirmEntry.set(null);
    this.voiding.set(null);
    this.deleting.set(null);
    this.confirmBusy.set(false);
  }

  /** Success closes the confirmation; a failure or conflict keeps it mounted
   * with the honest error so the retry can happen at the same revision. */
  protected async confirmVoid(): Promise<void> {
    const e = this.confirmEntry();
    if (!e || e.__typename !== 'ActivityEntry' || this.confirmBusy()) return;
    const a: ActivityEntry['activity'] = e.activity;
    this.confirmBusy.set(true);
    let ok = false;
    try {
      const res = await this.gql.mutate(VoidActivityDocument, {
        input: { id: a.id, expectedRevision: a.revision },
        clientMutationId: `${requestId()}:void-activity`,
      });
      ok = this.afterMutation(
        res,
        'voidActivity',
        'Activity voided — the record stays as history.',
      );
    } finally {
      this.confirmBusy.set(false);
    }
    if (ok) this.onConfirmDismissed();
  }

  protected async confirmDelete(): Promise<void> {
    const e = this.confirmEntry();
    if (!e || e.__typename !== 'MeasurementEntry' || this.confirmBusy()) return;
    const m: MeasurementEntry['measurement'] = e.measurement;
    this.confirmBusy.set(true);
    let ok = false;
    try {
      const res = await this.gql.mutate(DeleteMeasurementDocument, {
        input: { id: m.id, expectedRevision: m.revision },
        clientMutationId: `${requestId()}:delete-measurement`,
      });
      ok = this.afterMutation(res, 'deleteMeasurement', 'Measurement deleted.');
    } finally {
      this.confirmBusy.set(false);
    }
    if (ok) this.onConfirmDismissed();
  }

  /** true only when the mutation actually landed. */
  private afterMutation(
    res: GqlResult<Record<string, { problems?: PayloadProblem[] } | null>>,
    field: string,
    okLabel: string,
  ): boolean {
    if (res.kind === 'auth') {
      this.toasts.error('Session expired — sign in again.');
      return false;
    }
    if (res.kind !== 'ok') {
      this.toasts.error('Could not reach the server. Nothing changed.');
      return false;
    }
    const payload = res.data?.[field];
    const problems = (payload?.problems ?? []) as PayloadProblem[];
    if (problems.length > 0) {
      this.toasts.error(conflictNote(problems, 'This record') || problems[0].message);
      // conflict: the record moved under us, so the stale confirmation is
      // dropped once the refreshed truth lands
      if (problems.some((p) => p.code === 'CONFLICT')) {
        this.confirmEntry.set(null);
        this.voiding.set(null);
        this.deleting.set(null);
      }
      return false;
    }
    this.toasts.ok(okLabel);
    void this.store.refresh(); // reload the truth
    return true;
  }

  private fetchPage(after: string | null): Promise<GqlResult<Page<JournalItem>>> {
    const t = this.activeType();
    return this.gql
      .query(
        JournalDocument,
        {
          ...(this.plantId() ? { plantId: this.plantId() } : {}),
          first: 25,
          after,
          filter: {
            ...(t ? { entryTypes: [t] } : {}),
            ...(this.from() ? { from: new Date(this.from()).toISOString() } : {}),
            ...(this.until() ? { to: new Date(this.until()).toISOString() } : {}),
            ...(this.includeVoided() ? { includeTombstoned: true } : {}),
          },
        },
        this.abort.signal,
      )
      .then((res) => {
        if (res.kind !== 'ok') return res;
        if (!res.data) return { kind: 'ok', data: null, errors: res.errors };
        const conn = res.data.journal;
        this.hasNext.set(conn.pageInfo.hasNextPage);
        return {
          kind: 'ok',
          errors: res.errors,
          data: {
            items: conn.items,
            endCursor: conn.pageInfo.endCursor,
            hasNextPage: conn.pageInfo.hasNextPage,
          },
        };
      });
  }
}
