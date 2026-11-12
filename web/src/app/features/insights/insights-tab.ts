import { requestId } from '../../core/request-id';
import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  inject,
  input,
  signal,
  viewChild,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import {
  AnalysisRunStatusDocument,
  CancelAnalysisDocument,
  ProviderCapabilitiesDocument,
  RequestAnalysisDocument,
  SuggestionsPageDocument,
  type AnalysisRunFieldsFragment,
  type ProviderCapabilitiesQuery,
  type RequestAnalysisInput,
  type SuggestionFieldsFragment,
} from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiStatus } from '../../ui/status';
import { InsightSuggestionCard } from './suggestion-card';

type Caps = ProviderCapabilitiesQuery['providerCapabilities'];
type Run = AnalysisRunFieldsFragment;
type ObsResult = Extract<NonNullable<Run['result']>, { __typename: 'ObservationAnalysisResult' }>;

const TERMINAL = new Set<Run['state']>(['SUCCEEDED', 'FAILED', 'CANCELLED', 'STALE']);

/** The plant's photos, read off the journal's PHOTO member by the host page;
 * only the fields the pair picker needs are required. */
export type PickablePhoto = {
  id: string;
  takenAt: string | null;
  caption: string | null;
};

/**
 * Insights tab on plant detail (T34): the owner's window onto AI analysis
 * for this plant. Everything shown is a stored record — capability and
 * budget state come from ProviderCapabilities, the tracked run from
 * RequestAnalysis/AnalysisRunStatus (polled per contract: 2s, exponential
 * backoff to 15s, stop on terminal state or leave), and the suggestion
 * cards are the same PROPOSED rows the Today page shows.
 *
 * Honesty rules held here: every observation/hypothesis carries its
 * evidence chips or an explicit unsupported warning; hypotheses are
 * labelled as hypotheses; missing context is listed, not hidden; a
 * failed or cancelled run says so plainly; and nothing writes to care
 * except an explicit accept on a suggestion card.
 */
@Component({
  selector: 'insights-tab',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, UiStatus, UiEntryDialog, InsightSuggestionCard],
  templateUrl: './insights-tab.html',
  styleUrl: './insights-tab.css',
})
export class InsightsTab implements OnInit, OnDestroy {
  readonly plantId = input.required<string>();
  readonly photos = input<PickablePhoto[]>([]);

  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  private pollTimer: ReturnType<typeof setTimeout> | null = null;
  private pollDelay = 2000;

  protected readonly caps = signal<Caps | null>(null);
  protected readonly run = signal<Run | null>(null);
  protected readonly suggestions = signal<SuggestionFieldsFragment[]>([]);
  protected readonly picked = signal<string[]>([]);
  protected readonly note = signal('');
  protected readonly busy = signal(false);

  /** UI02: entry moves into the shared modal; the page keeps read-only
   * disclosure (engines, budget, results) plus the actions that open it.
   * Nothing is requested by opening — only the explicit submit does. */
  protected readonly reviewOpen = signal(false);
  protected readonly compareOpen = signal(false);
  protected readonly rerun = signal<Run['kind'] | null>(null);

  private readonly reviewEntry = viewChild<UiEntryDialog>('reviewEntry');
  private readonly compareEntry = viewChild<UiEntryDialog>('compareEntry');
  private readonly rerunEntry = viewChild<UiEntryDialog>('rerunEntry');

  /** One dialog at a time: the shared busy flag is what each one binds. */
  protected readonly anyDialogOpen = computed(
    () => this.reviewOpen() || this.compareOpen() || this.rerun() !== null,
  );

  protected readonly obsResult = computed<ObsResult | null>(() => {
    const r = this.run();
    return r?.state === 'SUCCEEDED' && r.result?.__typename === 'ObservationAnalysisResult'
      ? r.result
      : null;
  });
  protected readonly runIsLive = computed(() => {
    const r = this.run();
    return !!r && !TERMINAL.has(r.state);
  });
  protected readonly budgetSpent = computed(() => {
    const b = this.caps()?.budget;
    return b
      ? `${this.usd(b.monthSpentMicroUsd)} of ${this.usd(b.monthCeilingMicroUsd)} this month`
      : '';
  });
  protected readonly dailyExhausted = computed(() => {
    const b = this.caps()?.budget;
    return !!b && b.dailyRunsUsed >= b.dailyRunLimit;
  });

  async ngOnInit(): Promise<void> {
    await Promise.all([this.loadCaps(), this.loadSuggestions()]);
  }

  ngOnDestroy(): void {
    if (this.pollTimer !== null) clearTimeout(this.pollTimer);
    this.abort.abort();
  }

  protected async loadCaps(): Promise<void> {
    const r = await this.gql.query(ProviderCapabilitiesDocument, undefined, this.abort.signal);
    if (r.kind === 'ok' && r.data) this.caps.set(r.data.providerCapabilities);
  }

  protected async loadSuggestions(): Promise<void> {
    const r = await this.gql.query(
      SuggestionsPageDocument,
      { first: 10, filter: { plantId: this.plantId(), status: ['PROPOSED'] } },
      this.abort.signal,
    );
    if (r.kind === 'ok' && r.data) this.suggestions.set(r.data.suggestions.items);
  }

  protected evidenceFor(
    suggestion: SuggestionFieldsFragment,
  ): AnalysisRunFieldsFragment['evidence'] | null {
    const run = this.run();
    return run?.id === suggestion.runId ? run.evidence : null;
  }

  protected togglePhoto(id: string): void {
    this.picked.update((cur) =>
      cur.includes(id) ? cur.filter((p) => p !== id) : cur.length >= 2 ? cur : [...cur, id],
    );
  }

  // ---- opening the shared confirmation dialogs (never requests anything) ----

  protected openReview(): void {
    if (this.busy() || this.anyDialogOpen()) return;
    this.note.set('');
    this.reviewOpen.set(true);
  }

  protected openCompare(): void {
    if (this.busy() || this.anyDialogOpen() || this.photos().length < 2) return;
    this.note.set('');
    this.compareOpen.set(true);
  }

  protected openRerun(kind: Run['kind']): void {
    if (this.busy() || this.anyDialogOpen()) return;
    this.note.set('');
    // a VISION rerun sends the picked pair, so it needs a pair to send
    if (kind === 'VISION' && this.picked().length !== 2) return;
    this.rerun.set(kind);
  }

  /** Cancel/Escape on a confirmation that holds no draft: close it plainly. */
  protected closeReview(): void {
    this.reviewEntry()?.requestClose();
  }
  protected closeCompare(): void {
    this.compareEntry()?.requestClose();
  }
  protected closeRerun(): void {
    this.rerunEntry()?.requestClose();
  }

  protected onReviewDismissed(): void {
    this.reviewOpen.set(false);
  }
  protected onCompareDismissed(): void {
    this.compareOpen.set(false);
    // a cancelled comparison keeps its selection, so reopening continues where
    // the owner left off; the selection only clears on an accepted request
  }
  protected onRerunDismissed(): void {
    this.rerun.set(null);
  }

  protected async request(kind: 'SYNTHESIS' | 'VISION', force = false): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    this.note.set('');
    const input: RequestAnalysisInput = { kind, plantId: this.plantId() };
    if (kind === 'VISION') input.photoIds = this.picked();
    if (force) input.forceRerun = true;
    const r = await this.gql.mutate(
      RequestAnalysisDocument,
      {
        input,
        // unique per click: the server replays a stored response for a
        // reused clientMutationId, and dedup of identical analyses is the
        // semantic key's job, not this one
        clientMutationId: `${requestId()}:request-analysis`,
      },
      this.abort.signal,
    );
    this.busy.set(false);
    if (r.kind !== 'ok') {
      // honest failure: the confirmation stays mounted with its inputs
      this.note.set(
        r.kind === 'auth' ? 'Session expired — sign in again.' : 'Could not reach the server.',
      );
      return;
    }
    const p = r.data?.requestAnalysis;
    if (!p) {
      this.note.set('The server did not answer that request.');
      return;
    }
    const prob = p.problems?.[0];
    if (prob) {
      // IDEMPOTENCY_CONFLICT carries the existing run: following it is the
      // success path (03: no silent double-billing, but no dead end either).
      if (prob.code === 'IDEMPOTENCY_CONFLICT' && p.run) {
        this.note.set(prob.message);
        this.trackRun(p.run);
        this.closeRequestDialogs();
        return;
      }
      // budget/availability/other refusals: keep the confirmation open so the
      // reason appears next to the action that caused it
      this.note.set(prob.message);
      return;
    }
    if (p.run) {
      this.trackRun(p.run);
      // only an accepted request closes the confirmation
      this.closeRequestDialogs();
    }
  }

  /** A request landed: drop whichever confirmation(s) were open. The run
   * panel itself stays read-only on the page. */
  private closeRequestDialogs(): void {
    // markPristine first: the dialog's own input/change tracking saw the
    // photo checkboxes, and an accepted request is not a lost draft
    if (this.reviewOpen()) {
      this.reviewEntry()?.markPristine();
      this.reviewOpen.set(false);
    }
    if (this.compareOpen()) {
      this.compareEntry()?.markPristine();
      this.compareOpen.set(false);
    }
    if (this.rerun() !== null) {
      this.rerunEntry()?.markPristine();
      this.rerun.set(null);
    }
  }

  protected async cancel(): Promise<void> {
    const r0 = this.run();
    if (!r0 || this.busy()) return;
    this.busy.set(true);
    const r = await this.gql.mutate(
      CancelAnalysisDocument,
      { input: { runId: r0.id, expectedRevision: r0.revision } },
      this.abort.signal,
    );
    this.busy.set(false);
    if (r.kind !== 'ok') return;
    const p = r.data?.cancelAnalysis;
    if (p?.run) this.trackRun(p.run);
    else if (p?.problems?.[0]) this.note.set(p.problems[0].message);
  }

  /** A card changed: re-read the list honestly; if the server refused the
   * action, carry the explanation here — the stale card may disappear in
   * the re-read and its inline notice would vanish with it. */
  protected onSuggestionChanged(msg: string): void {
    if (msg) this.note.set(msg);
    void this.loadSuggestions();
  }

  /** Shared copy for the confirmations: what the engine actually does, and
   * what it costs, straight from ProviderCapabilities. */
  protected reviewSummary(): string {
    const c = this.caps();
    if (!c) return 'Engine state is still loading — the review stays disabled until it is known.';
    const bits = [
      c.text.available
        ? `Text analysis is available${c.text.model ? ` (${c.text.model})` : ''}.`
        : `No text engine is configured: ${c.text.unavailableReason ?? 'none configured.'}`,
      `Budget: ${c.budget.dailyRunsUsed} of ${c.budget.dailyRunLimit} runs today · ${this.budgetSpent()}.`,
    ];
    if (c.text.externalData)
      bits.push('This sends a snapshot of this plant’s records to an external engine.');
    return bits.join(' ');
  }

  protected compareSummary(): string {
    const c = this.caps();
    if (!c) return 'Engine state is still loading — comparison stays disabled until it is known.';
    const bits = [
      c.vision.available
        ? `Photo comparison is available${c.vision.model ? ` (${c.vision.model})` : ''}.`
        : `No vision engine is configured: ${c.vision.unavailableReason ?? 'none configured.'}`,
      `Budget: ${c.budget.dailyRunsUsed} of ${c.budget.dailyRunLimit} runs today · ${this.budgetSpent()}.`,
    ];
    if (c.vision.externalData) bits.push('The two photos you pick are sent to an external engine.');
    return bits.join(' ');
  }

  protected rerunSummary(): string {
    const c = this.caps();
    const budget = c
      ? `Budget: ${c.budget.dailyRunsUsed} of ${c.budget.dailyRunLimit} runs today · ${this.budgetSpent()}.`
      : '';
    return `An identical run may already exist for these inputs. Running again bills this run again — the last one stays on the record. ${budget}`;
  }

  protected rerunTitle(): string {
    return this.rerun() === 'VISION' ? 'Compare growth again' : 'Review history again';
  }

  protected isRerunBilled(): boolean {
    return this.rerun() !== null;
  }

  private trackRun(run: Run): void {
    if (this.pollTimer !== null) clearTimeout(this.pollTimer);
    this.pollDelay = 2000;
    this.run.set(run);
    if (!TERMINAL.has(run.state)) this.schedulePoll(run.id);
    else this.onTerminal(run);
  }

  /** A run that stopped changed the billing picture: re-read the budget
   * honestly, and pull in any suggestions the success projected. */
  private onTerminal(run: Run): void {
    void this.loadCaps();
    if (run.state === 'SUCCEEDED') void this.loadSuggestions();
  }

  private schedulePoll(runId: string): void {
    const delay = this.pollDelay;
    this.pollDelay = Math.min(delay * 2, 15000); // contract: 2s, back off to 15s
    this.pollTimer = setTimeout(() => void this.poll(runId), delay);
  }

  private async poll(runId: string): Promise<void> {
    const r = await this.gql.query(AnalysisRunStatusDocument, { runId }, this.abort.signal);
    if (r.kind !== 'ok' || !r.data?.analysisRun) return; // auth/leave: stop polling
    const run = r.data.analysisRun;
    this.run.set(run);
    if (TERMINAL.has(run.state)) {
      this.onTerminal(run);
      return;
    }
    this.schedulePoll(runId);
  }

  protected stateLabel(state: Run['state']): string {
    switch (state) {
      case 'QUEUED':
        return 'Queued — waiting for a worker';
      case 'RUNNING':
        return 'Running';
      case 'SUCCEEDED':
        return 'Done';
      case 'FAILED':
        return 'Failed';
      case 'CANCELLED':
        return 'Cancelled';
      case 'STALE':
        return 'Stale — the records behind it changed';
    }
  }

  protected stateKind(state: Run['state']): 'ok' | 'warn' | 'danger' {
    if (state === 'SUCCEEDED') return 'ok';
    if (state === 'FAILED') return 'danger';
    return 'warn';
  }

  protected evidenceKind(id: string): string {
    const ev = this.run()?.evidence;
    if (!ev) return 'record';
    if (ev.photoIds.includes(id)) return 'photo';
    if (ev.activityIds.includes(id)) return 'activity';
    if (ev.measurementIds.includes(id)) return 'measurement';
    if (ev.sourceVersionIds.includes(id)) return 'source';
    if (ev.weatherSnapshotIds.includes(id)) return 'weather snapshot';
    return 'record';
  }

  /** A two-photo evidence pair links straight into the compare view. */
  protected compareLink(id: string): { a: string; b: string } | null {
    const ev = this.run()?.evidence;
    if (!ev || !ev.photoIds.includes(id) || ev.photoIds.length !== 2) return null;
    const other = ev.photoIds.find((p) => p !== id);
    return other ? { a: id, b: other } : null;
  }

  protected usd(micro: string | null | undefined): string {
    if (micro === null || micro === undefined) return 'unknown';
    return `$${(Number(micro) / 1_000_000).toFixed(6)}`;
  }

  protected lower(v: string): string {
    return v.toLowerCase();
  }

  protected photoLabel(ph: PickablePhoto): string {
    return ph.caption || (ph.takenAt ? new Date(ph.takenAt).toLocaleDateString() : 'photo');
  }

  /** The rerun confirmation needs the same label, and it reads the photo
   * list rather than the caller's argument. */
  protected photoLabelById(id: string): string {
    const ph = this.photos().find((p) => p.id === id);
    return ph ? this.photoLabel(ph) : id;
  }
}
