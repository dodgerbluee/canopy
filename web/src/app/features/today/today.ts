import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { DatePipe } from '@angular/common';
import { Router, RouterLink } from '@angular/router';
import {
  CompleteOccurrenceDocument,
  CreatePlantDocument,
  PlantsDocument,
  ProviderCapabilitiesDocument,
  SnoozeOccurrenceDocument,
  SuggestionsPageDocument,
  TodayOverviewDocument,
  type PlantFieldsFragment,
  type ProviderCapabilitiesQuery,
  type SuggestionFieldsFragment,
  type TodayOverviewQuery,
} from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon } from '../../ui/icon';
import { UiPhoto } from '../../ui/photo';
import { UiPlantCard } from '../../ui/plant-card';
import { UiSkeleton } from '../../ui/skeleton';
import { UiStatus } from '../../ui/status';
import { UiToasts } from '../../ui/toast';
import { STARTER_PLANTS, starterKey } from '../collection/starter';
import { conflictNote, type PayloadProblem } from '../collection/shared';
import { HeroRotation } from './hero-rotation';
import { InsightSuggestionCard } from '../insights/suggestion-card';
import {
  dueNote,
  factLabel,
  groupAgenda,
  isCheck,
  kindLabel,
  localToday,
  missingFactsOf,
  unitsFor,
  type AgendaItem,
} from '../care/care-view';

type Overview = TodayOverviewQuery['todayOverview'];
type DueItem = Overview['dueTasks']['items'][number];

/** Cover derivative for the big hero panel. */
function displayUrl(p: PlantFieldsFragment): string | null {
  return p.coverPhoto ? `/api/media/${p.coverPhoto.asset.id}/display` : null;
}

/** Cover derivative for grid cards. */
function cardUrl(p: PlantFieldsFragment): string {
  return `/api/media/${p.coverPhoto!.asset.id}/medium`;
}

/**
 * Today dashboard (T28, UI01.b composition): one bounded todayOverview read
 * plus the existing Plants read. Everything on the page is a record that
 * already exists — the hero is a plant with an uploaded cover, the metric
 * strip counts loaded rows (and says so when pagination continues), and the
 * suggestions slot (T34) shows the workspace's open PROPOSED suggestions
 * with the engine state behind them. Nothing is
 * invented: no stock imagery, no lifetime totals, no weather claims.
 * Quick actions go through the frozen occurrence mutations; the whole
 * composition is re-read after every mutation so agenda and journal stay
 * coherent without local patching.
 */
@Component({
  selector: 'today-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    DatePipe,
    RouterLink,
    UiEntryDialog,
    UiIcon,
    UiPhoto,
    UiPlantCard,
    UiSkeleton,
    UiStatus,
    InsightSuggestionCard,
  ],
  templateUrl: './today.html',
  styleUrl: './today.css',
})
export class TodayPage implements OnDestroy {
  private readonly session = inject(SessionService);
  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  private readonly router = inject(Router);
  private readonly rotation = inject(HeroRotation);
  private readonly abort = new AbortController();

  protected readonly today = localToday();
  protected readonly loading = signal(true);
  protected readonly loadError = signal('');
  protected readonly data = signal<Overview | null>(null);
  protected readonly plantNames = signal(new Map<string, string>());
  /** the actual typed rows behind that name map — same read, no extra query */
  protected readonly plants = signal<PlantFieldsFragment[]>([]);
  protected readonly plantsTruncated = signal(false);
  /** T34: the workspace's open advice, straight off the suggestions table. */
  protected readonly openSuggestions = signal<SuggestionFieldsFragment[]>([]);
  protected readonly moreSuggestions = signal(false);
  protected readonly sugNote = signal('');
  protected readonly caps = signal<ProviderCapabilitiesQuery['providerCapabilities'] | null>(null);
  protected readonly opting = signal(false);
  protected readonly busy = signal(false);
  protected readonly activeId = signal<string | null>(null);
  protected readonly mode = signal<'complete' | 'snooze'>('complete');
  /** the occurrence the modal is about, kept separately from the list rows so
   * the composition re-read while it is open can never drop the form's context */
  private readonly activeItem = signal<DueItem | null>(null);
  /** the shared entry dialog, so the host can drive its guard */
  private readonly entry = viewChild<UiEntryDialog>('entry');
  private readonly revs = new Map<string, number>();
  /** this component instance's hero pick; null until the Plants read lands */
  private readonly heroId = signal<string | null>(null);

  // Template-facing helpers (pure functions from care-view).
  protected readonly isCheck = isCheck;
  protected readonly kindLabel = kindLabel;
  protected readonly dueNote = dueNote;
  protected readonly missingFactsOf = missingFactsOf;
  protected readonly factLabel = factLabel;
  protected readonly unitsFor = unitsFor;

  constructor() {
    void this.load();
  }

  ngOnDestroy(): void {
    this.abort.abort();
  }

  protected readonly plantCount = computed(() => this.plantNames().size);
  protected readonly dueCount = computed(() => this.data()?.dueTasks.items.length ?? 0);
  protected readonly entries = computed(() => this.data()?.recentEntries.items ?? []);
  protected readonly conditions = computed(() => this.data()?.conditions ?? null);

  /**
   * Hero pick: the plant this component chose, resolved against the loaded
   * rows. The choice itself is made once per Plants read by chooseHero() —
   * a round robin over the plants that actually have an uploaded cover, so the
   * page does not always open on the same one. If the chosen row disappears or
   * loses its cover, the existing-cover fallback and then the first real row
   * keep the panel honest; nothing is invented.
   */
  protected readonly hero = computed<PlantFieldsFragment | null>(() => {
    const rows = this.plants();
    const chosen = this.heroId();
    return (
      rows.find((p) => p.id === chosen && p.coverPhoto) ??
      rows.find((p) => p.coverPhoto) ??
      rows[0] ??
      null
    );
  });

  protected readonly heroPhoto = computed(() => {
    const p = this.hero();
    return p ? displayUrl(p) : null;
  });

  /** the hero eyebrow never renders undefined when a record omits category */
  protected heroLabel(p: PlantFieldsFragment): string {
    return [p.category?.toLowerCase() ?? 'plant', p.name].join(' · ');
  }

  /**
   * Hero headline: every plant is named as recorded, so the rotation is
   * visible in the title. The eyebrow and CTA carry the same real name.
   */
  protected heroTitle(p: PlantFieldsFragment): string {
    return p.name;
  }

  /** up to three cards, preferring plants that actually have covers */
  protected readonly featured = computed(() =>
    [...this.plants()]
      .sort((a, b) => Number(b.coverPhoto != null) - Number(a.coverPhoto != null))
      .slice(0, 3),
  );

  protected readonly photoTotal = computed(() =>
    this.plants().reduce((sum, p) => sum + (p.photoCount ?? 0), 0),
  );

  /** distinct location ids on the loaded rows — the same record, not a name match */
  protected readonly locationCount = computed(() => {
    const ids = new Set(
      this.plants()
        .map((p) => p.currentContext?.location?.id)
        .filter((id): id is string => !!id),
    );
    return ids.size;
  });

  protected readonly displayPlantCount = computed(() =>
    this.plantsTruncated() ? `${this.plantCount()}+` : String(this.plantCount()),
  );

  protected readonly dueLabel = computed(() =>
    this.data()?.dueTasks.pageInfo.hasNextPage ? `${this.dueCount()}+` : String(this.dueCount()),
  );

  protected readonly photoLabel = computed(() =>
    this.plantsTruncated() ? `${this.photoTotal()} in this view` : String(this.photoTotal()),
  );

  protected readonly locationLabel = computed(() =>
    this.plantsTruncated() ? `${this.locationCount()} in this view` : String(this.locationCount()),
  );

  protected cardUrl(p: PlantFieldsFragment): string {
    return cardUrl(p);
  }

  protected cardMeta(p: PlantFieldsFragment): string {
    const parts = [p.currentContext?.location?.name ?? 'Location not set'];
    if (p.currentContext) parts.push(p.currentContext.placement.toLowerCase());
    if (p.type === 'GROUP')
      parts.push(p.count != null ? `group (${p.count})` : 'group · count unknown');
    return parts.join(' · ');
  }

  protected cardSince(p: PlantFieldsFragment): string {
    const date = p.acquiredOn ?? p.createdAt?.slice(0, 10);
    if (!date) return '';
    const label = p.acquiredOn ? 'Acquired' : 'Added';
    const d = new Date(`${date}T00:00:00`);
    if (Number.isNaN(d.getTime())) return label;
    return `${label} ${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}`;
  }

  protected groups(): Array<{ key: string; label: string; items: DueItem[] }> {
    const groups = groupAgenda((this.data()?.dueTasks.items as AgendaItem[]) ?? [], this.today);
    return groups.map((g) => ({ ...g, items: g.items as DueItem[] }));
  }

  protected plantName(plantId: string): string {
    return this.plantNames().get(plantId) ?? 'unknown plant';
  }

  protected async reload(): Promise<void> {
    await this.load();
  }

  private async load(): Promise<void> {
    this.loading.set(true);
    this.loadError.set('');
    const [ov, plants] = await Promise.all([
      this.gql.query(TodayOverviewDocument, undefined, this.abort.signal),
      this.gql.query(PlantsDocument, { first: 100 }, this.abort.signal),
    ]);
    if (ov.kind === 'auth' || plants.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (ov.kind !== 'ok' || !ov.data || plants.kind !== 'ok' || !plants.data) {
      // a failed Plants read is as fatal as a failed overview: without it the
      // page would render a starter CTA or a zero garden over a real day
      this.loadError.set(this.failureNote(ov, plants));
      this.loading.set(false);
      return;
    }
    this.data.set(ov.data.todayOverview);
    if (plants.kind === 'ok' && plants.data) {
      const rows = plants.data.plants.items;
      this.plants.set(rows);
      this.plantNames.set(new Map(rows.map((p) => [p.id, p.name])));
      this.plantsTruncated.set(plants.data.plants.pageInfo.hasNextPage);
      this.chooseHero(rows);
    }
    // T34 reads are supplementary: a failed suggestions or capabilities read
    // leaves the day intact and the slot says what it cannot see.
    void this.reloadSuggestions();
    this.loading.set(false);
  }

  /**
   * Hero choice for this Plants read. A component instance picks once: the
   * same page completing/snoozing/reloading keeps its cover, while a fresh
   * TodayPage on navigation rotates to the next plant with an uploaded cover.
   * A hard refresh resumes from the last pick the rotation service recorded
   * for this workspace. No covers at all: no pick, and the hero computed falls
   * back to the first real row (placeholder, never fake imagery).
   */
  private chooseHero(rows: PlantFieldsFragment[]): void {
    const current = this.heroId();
    if (current !== null && rows.some((p) => p.id === current && p.coverPhoto)) return;
    const eligible = rows.filter((p) => p.coverPhoto).map((p) => p.id);
    this.heroId.set(this.rotation.pick(this.session.info()?.workspaceId ?? '', eligible));
  }

  /**
   * Load-failure diagnostics (UI03): when the server answered with a GraphQL
   * error envelope, show its first error message — the backend already
   * sanitizes these, and the live failure was invisible behind the generic
   * label. A real transport failure keeps the network fallback. This only
   * surfaces what the server said; it claims no root cause.
   */
  private failureNote(
    ...results: Array<Extract<GqlResult<unknown>, { kind: 'ok' | 'network' | 'aborted' }>>
  ): string {
    const first = results
      .map((r) => (r.kind === 'ok' ? (r.errors[0]?.message ?? '') : ''))
      .find((m) => m.trim() !== '');
    return first ? `${first} (server GraphQL error)` : 'Could not reach the server.';
  }

  /** Re-reads open suggestions plus engine state; used on load and after
   * any accept/dismiss so the slot never shows stale rows. */
  protected onSuggestionChanged(msg: string): void {
    // a refused action's explanation lives here, not on the stale card:
    // the re-read below may remove that card entirely
    this.sugNote.set(msg);
    void this.reloadSuggestions();
  }
  protected async reloadSuggestions(): Promise<void> {
    const [sugg, caps] = await Promise.all([
      this.gql.query(
        SuggestionsPageDocument,
        { first: 5, filter: { status: ['PROPOSED'] } },
        this.abort.signal,
      ),
      this.gql.query(ProviderCapabilitiesDocument, undefined, this.abort.signal),
    ]);
    if (sugg.kind === 'ok' && sugg.data) {
      this.openSuggestions.set(sugg.data.suggestions.items);
      this.moreSuggestions.set(sugg.data.suggestions.pageInfo.hasNextPage);
    }
    if (caps.kind === 'ok' && caps.data) this.caps.set(caps.data.providerCapabilities);
  }

  protected startComplete(o: DueItem): void {
    this.activeId.set(o.id);
    this.activeItem.set(o);
    this.mode.set('complete');
  }

  protected startSnooze(o: DueItem): void {
    this.activeId.set(o.id);
    this.activeItem.set(o);
    this.mode.set('snooze');
  }

  /** The occurrence the modal is bound to. */
  protected entryItem(): DueItem | null {
    return this.activeItem();
  }

  protected entryTitle(): string {
    return this.mode() === 'snooze' ? 'Snooze care' : 'Record care';
  }

  protected entryDescription(): string {
    const o = this.activeItem();
    const what = o ? `${o.ruleKey} · ${this.plantName(o.plantId)}` : 'this task';
    return this.mode() === 'snooze'
      ? `Move ${what} to a later date — the original date stays on the card.`
      : `Log ${what} in the plan’s own words.`;
  }

  /** Cancel/X/Escape/backdrop all go through the shared dirty guard; the owner
   * clears the row state only once that guard resolves true. */
  protected onEntryDismissed(): void {
    this.activeId.set(null);
    this.activeItem.set(null);
    this.busy.set(false);
  }

  /** Cancel button: the shared guard decides, never a direct state clear. */
  protected requestCancel(): void {
    this.entry()?.requestClose();
  }

  /** A save landed: approve the draft as clean so the programmatic close that
   * follows is never answered with "discard changes?". */
  protected markEntryPristine(): void {
    this.entry()?.markPristine();
  }

  protected async confirmComplete(o: DueItem, el: HTMLFormElement): Promise<void> {
    if (this.busy()) return;
    const data = Object.fromEntries(new FormData(el));
    const input: Record<string, unknown> = {
      occurrenceId: o.id,
      expectedRevision: this.revs.get(o.id) ?? 1,
    };
    const units = unitsFor(o.activityKind);
    if (units.length) {
      const qty = String(data['quantity'] ?? '').trim();
      if (!/^\d+(\.\d+)?$/.test(qty)) {
        this.toasts.error('Enter a decimal quantity — the log never guesses one.');
        return;
      }
      input['quantity'] = qty;
      input['unit'] = data['unit'];
    }
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(CompleteOccurrenceDocument, {
        input,
        clientMutationId: `${crypto.randomUUID()}:complete-occurrence`,
      } as never);
      this.afterOp(
        o,
        res,
        'completeOccurrence',
        isCheck(o) ? 'Checked — logged to the journey.' : 'Completed — logged to the journey.',
      );
    } finally {
      this.busy.set(false);
    }
  }

  protected async confirmSnooze(o: DueItem, el: HTMLFormElement): Promise<void> {
    if (this.busy()) return;
    const until = String(Object.fromEntries(new FormData(el))['until'] ?? '');
    if (!/^\d{4}-\d{2}-\d{2}$/.test(until)) {
      this.toasts.error('Pick the date to snooze until.');
      return;
    }
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(SnoozeOccurrenceDocument, {
        input: {
          occurrenceId: o.id,
          expectedRevision: this.revs.get(o.id) ?? 1,
          until,
        },
        clientMutationId: `${crypto.randomUUID()}:snooze-occurrence`,
      });
      this.afterOp(
        o,
        res,
        'snoozeOccurrence',
        `Snoozed to ${until} — the original date stays on the card.`,
      );
    } finally {
      this.busy.set(false);
    }
  }

  private afterOp(
    o: DueItem,
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
    const problems = (res.data?.[field]?.problems ?? []) as PayloadProblem[];
    const conflict = problems.find((p) => p.code === 'CONFLICT');
    if (conflict) {
      // the modal stays mounted with the entered values and the corrected
      // revision, so the retry can happen at the current revision
      if (conflict.currentRevision != null) this.revs.set(o.id, conflict.currentRevision);
      this.toasts.error(conflictNote([conflict], 'This task'));
      return false;
    }
    if (problems.length > 0) {
      this.toasts.error(problems[0].message);
      return false;
    }
    this.revs.set(o.id, (this.revs.get(o.id) ?? 1) + 1);
    this.toasts.ok(okLabel);
    // the mutation landed: the form is no longer a draft, so closing it is
    // never answered with "discard changes?"
    this.markEntryPristine();
    this.activeId.set(null);
    this.activeItem.set(null);
    void this.load(); // re-read the whole composition — agenda and journal move together
    return true;
  }

  /** Opt-in starter set: idempotent per plant, so a retried click converges. */
  protected async optIn(): Promise<void> {
    if (this.opting()) return;
    this.opting.set(true);
    try {
      for (const s of STARTER_PLANTS) {
        const res = await this.gql.mutate(CreatePlantDocument, {
          input: {
            name: s.name,
            cultivar: s.cultivar,
            type: s.type,
            count: s.count,
            category: s.category,
          },
          clientMutationId: starterKey(s),
        });
        if (res.kind === 'auth') {
          this.session.markExpired();
          return;
        }
        if (res.kind !== 'ok') {
          this.toasts.error('Could not reach the server. The starter set was not completed.');
          return;
        }
        const problems = res.data?.createPlant?.problems ?? [];
        const blocking = problems.filter((p) => p.code !== 'IDEMPOTENCY_CONFLICT');
        if (blocking.length > 0) {
          this.toasts.error(`Starter set stopped at “${s.name}”: ${blocking[0].message}`);
          return;
        }
      }
      this.toasts.ok('Starter collection added — five trees and two groups');
      await this.router.navigate(['/plants']); // show what was added, as the root screen always has
    } finally {
      this.opting.set(false);
    }
  }
}
