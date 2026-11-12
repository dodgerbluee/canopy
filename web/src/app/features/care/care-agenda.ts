import { requestId } from '../../core/request-id';
import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  inject,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { RouterLink, RouterOutlet } from '@angular/router';
import {
  CareAgendaDocument,
  CompleteOccurrenceDocument,
  PlantsDocument,
  SkipOccurrenceDocument,
  SnoozeOccurrenceDocument,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { PageStore, type Page } from '../../core/graphql/page-store';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiSkeleton } from '../../ui/skeleton';
import { UiToasts } from '../../ui/toast';
import { conflictNote, parseDecimal, type PayloadProblem } from '../collection/shared';
import { CareGuidance } from './care-guidance';
import {
  dueNote,
  factLabel,
  groupAgenda,
  isCheck,
  kindLabel,
  localToday,
  missingFactsOf,
  offsetDate,
  unitsFor,
  type AgendaItem,
} from './care-view';

/**
 * Care agenda (T25): due work grouped Overdue / Today / Upcoming against
 * the browser's local day, over the frozen keyset connection. Checks and
 * actions are visibly distinct; snoozed rows keep the original date in
 * view; missing context is named, never guessed around. Completing an
 * action can carry quantity/unit/note onto the activity created with it.
 */
@Component({
  selector: 'care-agenda-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, RouterOutlet, UiEntryDialog, UiSkeleton, CareGuidance],
  templateUrl: './care-agenda.html',
  styleUrl: './care-agenda.css',
})
export class CareAgendaPage implements OnInit, OnDestroy {
  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  private readonly abort = new AbortController();

  protected readonly store: PageStore<AgendaItem>;
  private readonly plantNames = signal(new Map<string, string>());
  /** Optimistic revisions: fresh rows start at 1; a CONFLICT's
   * currentRevision hint corrects the map so the retry can succeed. */
  private readonly revs = new Map<string, number>();

  protected readonly activeId = signal<string | null>(null);
  protected readonly mode = signal<'complete' | 'snooze' | 'skip'>('complete');
  /** the occurrence the modal is about, kept separately from the list rows so
   * a refresh while the dialog is open can never drop the form's context */
  private readonly activeItem = signal<AgendaItem | null>(null);
  protected readonly busy = signal(false);
  /** The quick-action modal, so the host can drive its shared guard. */
  private readonly entry = viewChild<UiEntryDialog>('entry');
  protected readonly hasNext = signal(false);
  protected readonly unitsFor = unitsFor;
  protected readonly offsetDate = offsetDate;
  protected readonly isCheck = isCheck;
  protected readonly kindLabel = kindLabel;
  protected readonly dueNote = dueNote;
  protected readonly missingFactsOf = missingFactsOf;
  protected readonly factLabel = factLabel;

  constructor() {
    this.store = new PageStore((after) => this.fetchPage(after));
  }

  ngOnInit(): void {
    void this.store.refresh();
    void this.loadPlantNames();
  }

  ngOnDestroy(): void {
    this.abort.abort();
    this.store.destroy();
  }

  protected readonly groups = computed(() => groupAgenda(this.store.items(), localToday()));

  protected plantName(id: string): string {
    return this.plantNames().get(id) ?? 'a plant';
  }

  protected retry(): void {
    void this.store.refresh();
  }

  /** The plan editor modal renders in this page's outlet (nested in a later
   * pass). When it deactivates — saved or dismissed — re-read the agenda so
   * a new plan's work shows up immediately. */
  protected onOutletDeactivate(): void {
    void this.store.refresh();
  }

  protected startComplete(o: AgendaItem): void {
    this.activeId.set(o.id);
    this.activeItem.set(o);
    this.mode.set('complete');
  }

  protected startSnooze(o: AgendaItem): void {
    this.activeId.set(o.id);
    this.activeItem.set(o);
    this.mode.set('snooze');
  }

  protected startSkip(o: AgendaItem): void {
    this.activeId.set(o.id);
    this.activeItem.set(o);
    this.mode.set('skip');
  }

  /** The occurrence the modal is bound to. */
  protected entryItem(): AgendaItem | null {
    return this.activeItem();
  }

  /** Modal titles/descriptions per mode — human readable, and skip stays an
   * explicit confirmation rather than a silent mutation. */
  protected entryTitle(): string {
    const m = this.mode();
    return m === 'snooze' ? 'Snooze care' : m === 'skip' ? 'Skip occurrence' : 'Record care';
  }

  protected entryDescription(): string {
    const o = this.activeItem();
    const what = o ? `${o.ruleKey} · ${this.plantName(o.plantId)}` : 'this task';
    switch (this.mode()) {
      case 'snooze':
        return `Move ${what} to a later date — the original date stays on the card.`;
      case 'skip':
        return `Skip ${what}? The plan stays; only this one date is passed over.`;
      default:
        return `Log ${what} in the plan’s own words.`;
    }
  }

  /** Cancel/X/Escape/backdrop all go through the shared dirty guard; the owner
   * clears the two-step row state only once that guard resolves true. */
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

  protected async confirmComplete(o: AgendaItem, el: HTMLFormElement): Promise<void> {
    if (this.busy()) return;
    const data = Object.fromEntries(new FormData(el));
    const qty = parseDecimal(String(data['quantity'] ?? ''));
    if (qty.error) {
      this.toasts.error(qty.error ?? 'Invalid quantity.');
      return;
    }
    if (qty.value !== null && !data['unit']) {
      this.toasts.error('Pick a unit for the quantity.');
      return;
    }
    this.busy.set(true);
    try {
      const input: Record<string, unknown> = {
        occurrenceId: o.id,
        expectedRevision: this.revs.get(o.id) ?? 1,
      };
      const note = String(data['note'] ?? '').trim();
      if (note) input['note'] = note;
      if (qty.value !== null) {
        input['quantity'] = qty.value;
        input['unit'] = data['unit'];
      }
      const res = await this.gql.mutate(CompleteOccurrenceDocument, {
        input,
        clientMutationId: `${requestId()}:complete-occurrence`,
      } as never);
      this.afterOp(
        o,
        res,
        'completeOccurrence',
        isCheck(o) ? 'Checked — logged to the journey.' : 'Completed.',
      );
    } finally {
      this.busy.set(false);
    }
  }

  protected async confirmSnooze(o: AgendaItem, el: HTMLFormElement): Promise<void> {
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
        clientMutationId: `${requestId()}:snooze-occurrence`,
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

  protected async confirmSkip(o: AgendaItem): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    try {
      const res = await this.gql.mutate(SkipOccurrenceDocument, {
        input: {
          occurrenceId: o.id,
          expectedRevision: this.revs.get(o.id) ?? 1,
        },
        clientMutationId: `${requestId()}:skip-occurrence`,
      });
      this.afterOp(o, res, 'skipOccurrence', 'Skipped — the plan continues from the next date.');
    } finally {
      this.busy.set(false);
    }
  }

  private afterOp(
    o: AgendaItem,
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
    void this.store.refresh(); // reload the truth
    return true;
  }

  private async loadPlantNames(): Promise<void> {
    const res = await this.gql.query(PlantsDocument, { first: 100 }, this.abort.signal);
    if (res.kind === 'ok' && res.data) {
      this.plantNames.set(new Map(res.data.plants.items.map((p) => [p.id, p.name])));
    }
  }

  private fetchPage(after: string | null): Promise<GqlResult<Page<AgendaItem>>> {
    const today = localToday();
    return this.gql
      .query(
        CareAgendaDocument,
        {
          from: offsetDate(today, -30),
          to: offsetDate(today, 60),
          first: 50,
          after,
        },
        this.abort.signal,
      )
      .then((res) => {
        if (res.kind !== 'ok') return res;
        if (!res.data) return { kind: 'ok', data: null, errors: res.errors };
        const conn = res.data.careAgenda;
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
