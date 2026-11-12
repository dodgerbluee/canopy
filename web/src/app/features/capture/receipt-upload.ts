import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import {
  AnalysisRunStatusDocument,
  AttachReceiptDocument,
  ProviderCapabilitiesDocument,
  RequestAnalysisDocument,
  type AnalysisRunFieldsFragment,
  type AnalysisRunState,
  type AnalysisRunStatusQuery,
  type ProblemCode,
  type RequestAnalysisInput,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiStatus } from '../../ui/status';
import { Uploader, type MediaProblem } from './upload';

/** The receipt draft as surfaced to the purchase editor: the contract's
 * ReceiptExtractionResult plus the run's provenance (provider/model), so
 * the owner can see which engine produced each value. */
export type ReceiptDraft = Extract<
  NonNullable<AnalysisRunFieldsFragment['result']>,
  { __typename: 'ReceiptExtractionResult' }
> & { provider: string | null; model: string | null };

/** A READY receipt image the editor owns but has not attached yet — UI09:
 * on a new purchase there is no purchase to attach to, so the asset waits
 * here until the parent saves and attaches it itself. */
export type StagedReceipt = { assetId: string; filename: string };

type ReceiptCap = {
  available: boolean;
  unavailableReason: string | null;
  externalData: boolean;
};

type RunView = Pick<
  AnalysisRunFieldsFragment,
  'id' | 'state' | 'failureCode' | 'failureMessage' | 'result' | 'provider' | 'model'
>;

type RequestProblem = { code: ProblemCode; message: string };

/** The one request body plus its key for the extraction attempt currently in
 * play. Uncertain transport retries resend exactly this pair, so the server
 * replays the run it already accepted instead of queueing (and billing) a
 * second one. */
type Attempt = {
  input: RequestAnalysisInput;
  clientMutationId: string;
};

/** The only problem code whose payload `run` may be followed. Any other
 * problem — or GraphQL errors — is a refusal, even when a run is present. */
const REUSABLE_PROBLEM: ProblemCode = 'IDEMPOTENCY_CONFLICT';

/** Honest endings per situation. Never claims an unsaved purchase is done. */
const NEW_PURCHASE_ENDING =
  ' Your receipt and purchase draft are still here. You can retry or enter ' +
  'the details by hand.';
const SAVED_PURCHASE_ENDING = ' The saved purchase is unchanged.';

/**
 * Receipt photo slot (T32.a): one optional image per purchase line of work —
 * upload never blocks the manual purchase. Same reserve → PUT → finalize →
 * processing pipeline as photos, purpose RECEIPT.
 *
 * T32.b adds the optional OCR review path: once a receipt is ready the owner
 * may ask for extraction (local OCR; the contract keeps it a draft that never
 * writes anything). Extraction is strictly advisory — capability unavailable,
 * failure or abandonment leaves the manual purchase untouched. The run needs
 * only a READY receipt asset (`RequestAnalysisInput.receiptAssetId`), so it
 * works before any purchase exists.
 *
 * UI02 (receipt entry consistency): the extraction request is confirmed in
 * the shared UiEntryDialog. Opening that dialog never bills anything — only
 * the explicit Extract details action inside it calls the same extract()
 * method. The confirmation stays mounted while the run is in flight, so a
 * failure keeps its honest error visible and the same action retries; it
 * closes only on an accepted request. The resulting draft is reviewed by the
 * purchase editor (its own shared modal), so nothing here renders a draft.
 *
 * UI09 (receipt first): `purchaseId` is optional. Without one, a READY
 * receipt enters the "ready" state and is emitted through `staged` — the
 * parent owns the staged list and attaches the asset itself after the
 * purchase exists. With one (an existing purchase) the old immediate attach +
 * `attached` behaviour stands. Readiness is never re-queried here: the
 * pending set only ever lists RESERVED/UPLOADED/PROCESSING/FAILED, so
 * extraction and attach are validated by the server, as before.
 *
 * `disabled` is the parent's save/upload pass: while it is true the slot's
 * own affordances stay locked, so an upload can never race a save.
 */
@Component({
  selector: 'receipt-upload',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [UiEntryDialog, UiStatus],
  template: `
    <div class="receipt-slot" role="note">
      <span class="caption">
        @switch (state()) {
          @case ('idle') {
            Start with a receipt to fill in the details, or enter them below.
            @if (!purchaseId()) {
              Add it first: you can extract details from it before the purchase is saved.
            }
          }
          @case ('uploading') {
            Uploading receipt image… {{ pct() }}%
          }
          @case ('processing') {
            Server is processing the image…
          }
          @case ('attached') {
            Receipt image attached.
          }
          @case ('ready') {
            Receipt image saved — it attaches when you save the purchase.
          }
          @case ('failed') {
            {{ error() }}
          }
        }
      </span>
      @if (state() !== 'idle') {
        <ui-status
          [kind]="
            state() === 'attached' || state() === 'ready'
              ? 'ok'
              : state() === 'failed'
                ? 'danger'
                : 'warn'
          "
          [label]="
            state() === 'attached'
              ? 'Attached'
              : state() === 'ready'
                ? 'Ready to attach'
                : state() === 'failed'
                  ? 'Failed'
                  : state() === 'uploading'
                    ? 'Uploading'
                    : 'Processing'
          "
        />
      }
      @if (state() === 'attached' || state() === 'ready') {
        <div class="receipt-file caption">
          {{ selectedName() }}
          <a [href]="'/api/media/' + lastAssetId() + '/original'">View original</a>
        </div>
      }
      <!-- Real button, not a styled label: it is the receipt-first action, so
           it is reachable by keyboard and takes the dialog's initial focus.
           It does not open the picker itself — the visually-only file input
           stays the element that reads the chosen file. -->
      <button
        type="button"
        class="btn"
        [disabled]="uploadLocked()"
        (click)="receiptFile.click()"
        cdkFocusInitial
      >
        Add receipt photo
      </button>
      <input
        #receiptFile
        type="file"
        accept="image/*"
        aria-label="Receipt photo"
        class="visually-only"
        [disabled]="uploadLocked()"
        (change)="pick($event)"
      />
    </div>
    @if (state() === 'failed') {
      <button type="button" class="btn" style="margin-top:var(--sp-2)" (click)="state.set('idle')">
        Try again
      </button>
    }
    @if (state() === 'attached' || state() === 'ready') {
      <div class="ocr-row" aria-live="polite">
        @switch (extractState()) {
          @case ('idle') {
            @if (cap(); as c) {
              @if (c.available) {
                <span class="caption">
                  @if (c.externalData) {
                    Extraction can send this receipt to an external model.
                  } @else {
                    Extraction runs locally on this deployment.
                  }
                </span>
                <button type="button" class="btn" [disabled]="disabled()" (click)="openRequest()">
                  Extract details from receipt
                </button>
              } @else {
                <span class="caption">
                  Text extraction is unavailable:
                  {{ c.unavailableReason ?? 'not configured' }} — the purchase is complete without
                  it.
                </span>
              }
            } @else {
              <button type="button" class="btn" [disabled]="disabled()" (click)="openRequest()">
                Extract details from receipt
              </button>
            }
          }
          @case ('requesting') {
            <span class="caption">Starting extraction…</span>
            <ui-status kind="warn" label="Starting" />
          }
          @case ('running') {
            <span class="caption">Reading the receipt — this can take a moment…</span>
            <ui-status kind="warn" label="Reading" />
          }
          @case ('draft') {
            <span class="caption">Draft ready — review it in the extracted-details dialog.</span>
            <ui-status kind="ok" label="Draft ready" />
            <button type="button" class="btn" (click)="draft() && extracted.emit(draft()!)">
              Review extracted details
            </button>
          }
          @case ('failed') {
            <span class="caption">{{ extractError() }}</span>
            <ui-status kind="danger" label="Extraction failed" />
            <button type="button" class="btn" [disabled]="disabled()" (click)="openRequest()">
              Try extraction again
            </button>
          }
        }
      </div>
    }

    <!-- Extraction request: shared confirmation. Opening it bills nothing;
         only Extract details inside calls extract(). It stays mounted while
         the request/run is in flight and closes on an accepted request. -->
    <ui-entry-dialog
      #requestEntry
      [open]="requesting()"
      title="Extract details from receipt"
      [description]="requestDescription()"
      size="compact"
      [busy]="extractBusy()"
      (dismissed)="closeRequest()"
    >
      @if (requesting()) {
        <div class="entry-form">
          <div class="form-section">
            <p class="field-hint">
              This reads the receipt image and returns a draft. Nothing is written to the purchase
              until you apply the draft yourself.
            </p>
            @if (cap(); as c) {
              <p class="field-hint">
                @if (c.externalData) {
                  This receipt image will be sent to an external model.
                } @else {
                  Extraction runs locally on this deployment.
                }
              </p>
            } @else {
              <p class="field-hint">
                This deployment's extraction engine is not known yet — the run reports honestly if
                none is configured.
              </p>
            }
            @if (extractState() === 'failed' && extractError()) {
              <p role="status" class="field-error">{{ extractError() }}</p>
            }
            @if (retryNotice()) {
              <p class="field-hint">{{ retryNotice() }}</p>
            }
          </div>
          <div class="form-actions">
            <button type="button" class="btn" (click)="closeRequest()">Cancel</button>
            <button
              type="button"
              class="btn btn-primary"
              [disabled]="extractState() !== 'idle' && extractState() !== 'failed'"
              (click)="extract()"
            >
              {{
                extractState() === 'failed'
                  ? 'Try extraction again'
                  : extractState() === 'idle'
                    ? 'Extract details'
                    : 'Extracting…'
              }}
            </button>
          </div>
        </div>
      }
    </ui-entry-dialog>
  `,
  styles: [
    `
      .receipt-slot {
        display: flex;
        flex-wrap: wrap;
        justify-content: space-between;
        align-items: center;
        gap: var(--sp-3);
        border: 1px dashed var(--border);
        border-radius: var(--radius-card);
        padding: var(--sp-3);
        margin-top: var(--sp-3);
      }
      .ocr-row {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: var(--sp-3);
        margin-top: var(--sp-2);
      }
      .receipt-file {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: var(--sp-2);
        min-width: 0;
        /* long camera filenames have no break opportunity — let them wrap
           inside the narrow phone modal instead of overflowing the slot */
        overflow-wrap: anywhere;
      }
    `,
  ],
})
export class ReceiptUpload {
  /** Present only when a purchase already exists (edit route). Without it a
   * READY receipt is staged for the parent to attach after it saves. */
  readonly purchaseId = input<string | null>(null);
  /** the parent's save/attach pass is in flight: the slot stays locked. */
  readonly disabled = input(false);
  /** Existing purchase only: the receipt is already attached server-side. */
  readonly attached = output<void>();
  /** A READY receipt the parent owns and must attach itself (new purchase).
   * The parent owns the staged array; this emits once per finished upload. */
  readonly staged = output<StagedReceipt>();
  /** Emitted when an extraction run reaches SUCCEEDED with a receipt draft,
   * and again when the owner asks to look at that same draft — the purchase
   * editor owns the review UI from there. Replays carry the identical cached
   * draft, so reviewing never requests (or bills) a second run. */
  readonly extracted = output<ReceiptDraft>();
  /** Emitted when a new upload begins: any earlier extraction review belongs
   * to a different image, so the parent closes it instead of keeping a stale
   * draft on screen. */
  readonly started = output<void>();
  /** true while an upload, a processing wait or an extraction run is in
   * flight — the parent folds this into its own busy state so a save can
   * never race a receipt upload. */
  readonly busy = output<boolean>();

  private readonly uploader = inject(Uploader);
  private readonly gql = inject(GraphQLClient);
  private readonly destroyRef = inject(DestroyRef);
  private readonly pickId = `ru-pick-${++seq}`;
  /** false once the component is gone: late polls stop emitting. */
  private alive = true;

  protected readonly state = signal<
    'idle' | 'uploading' | 'processing' | 'attached' | 'ready' | 'failed'
  >('idle');
  protected readonly pct = signal(0);
  protected readonly error = signal('');

  protected readonly cap = signal<ReceiptCap | null>(null);
  protected readonly extractState = signal<'idle' | 'requesting' | 'running' | 'draft' | 'failed'>(
    'idle',
  );
  protected readonly extractError = signal('');
  /** declarative [open] of the request confirmation. It is closed through
   * this signal (never through the dialog's own guard), so a new upload can
   * drop a stale confirmation without a discard prompt. */
  protected readonly requesting = signal(false);
  protected readonly lastAssetId = signal('');
  /** the image the owner chose, shown once it is attached/ready */
  protected readonly selectedName = signal('');
  /** the exact draft from the last successful run, exposed to the template so
   * the Review action can replay it through the same extracted output —
   * reads only what a successful run already produced, no request, no bill.
   * Cleared when a new receipt upload starts, so a stale draft can never be
   * reviewed against a different image. */
  protected readonly draft = signal<ReceiptDraft | null>(null);
  /** upload/processing in flight — a signal, so the locks and the busy
   * output re-evaluate instead of reading a stale plain bool. */
  private readonly uploading = signal(false);

  /** The attempt currently in play: its exact body and key, kept so an
   * uncertain response can be resent unchanged. */
  private attempt: Attempt | null = null;
  /** true only when the last attempt ended in an explicit terminal run state
   * (FAILED/STALE/CANCELLED), which is what makes the next owner-confirmed
   * request a forceRerun with a fresh key. Nothing else sets this: a lost
   * response, a network error, a polling timeout or a run-read failure says
   * nothing about the run, so those never qualify and never rotate the key. */
  private terminalRetry = false;
  /** the terminal state behind `terminalRetry`, so the wording can name what
   * actually happened instead of guessing. */
  private terminalRunState: AnalysisRunState | null = null;
  /** why a FAILED run failed, kept for the confirmation's honest reason. */
  private terminalFailureReason = '';

  /** true when the owner's next explicit attempt should start a genuinely new
   * read (forceRerun + a fresh key) rather than resend the same request. */
  private rerunArmed(): boolean {
    return this.terminalRetry && this.attempt !== null;
  }

  /** Extra confirmation copy for the state being retried. A forceRerun is a
   * new run, so it says so and says it may count against the limits. */
  private retryNotice(): string {
    if (!this.rerunArmed()) return '';
    const prior =
      this.terminalRunState === 'STALE'
        ? 'The previous read was invalidated before it finished.'
        : this.terminalRunState === 'CANCELLED'
          ? 'The previous read was cancelled.'
          : `The previous read failed. ${this.terminalFailureReason}`.trim();
    return `${prior} Retrying starts a new read of this receipt, which may count against this deployment's analysis limits.`;
  }

  /** The request confirmation: Cancel/X never need a discard prompt, because
   * opening this dialog has not requested (or billed) anything yet. A failed
   * run is idle again, so the dialog stays closable and its action re-arms. */
  private readonly requestEntry = viewChild<UiEntryDialog>('requestEntry');

  protected requestDescription(): string {
    const c = this.cap();
    return c
      ? c.externalData
        ? 'Sends this receipt image to an external model'
        : 'Extraction runs locally on this deployment'
      : 'Engine for this deployment not confirmed yet';
  }

  /** Opens the confirmation only — the request itself waits for the explicit
   * action inside the dialog. A failed run re-arms from here: the state goes
   * back to idle, the old error is cleared, and the dialog stays open with a
   * live Extract action. */
  protected openRequest(): void {
    if (this.disabled() || this.uploading()) return;
    if (!this.lastAssetId()) return;
    const failed = this.extractState() === 'failed';
    if (this.extractState() !== 'idle' && !failed) return;
    if (failed) {
      this.extractState.set('idle');
      this.extractError.set('');
      this.emitBusy();
    }
    this.requesting.set(true);
  }

  /** Cancel/X/backdrop: refused only while a request or run is actually in
   * flight. A failed run keeps its honest error visible in the dialog, but
   * the dialog is closable again and the page's retry action re-arms it. */
  protected closeRequest(): void {
    if (this.extractBusy()) return;
    this.requestEntry()?.markPristine();
    this.requesting.set(false);
  }

  protected pick(ev: Event): void {
    const input = ev.target as HTMLInputElement;
    const file = Array.from(input.files ?? [])[0];
    input.value = '';
    if (file) void this.run(file);
  }

  /** Upload is possible only when nothing (here or in the parent) is in
   * flight. A finished draft does not hold the slot: choosing another receipt
   * after one run simply drops that draft, which is the owner's own call. */
  protected readonly uploadLocked = computed(
    () =>
      this.disabled() ||
      this.uploading() ||
      this.state() === 'uploading' ||
      this.state() === 'processing' ||
      this.extractState() === 'requesting' ||
      this.extractState() === 'running',
  );

  /** true while upload/processing or an extraction request/run is in flight. */
  private readonly extractBusy = computed(
    () => this.extractState() === 'requesting' || this.extractState() === 'running',
  );

  private emitBusy(): void {
    if (!this.alive) return;
    this.busy.emit(this.uploading() || this.extractBusy());
  }

  private async run(file: File): Promise<void> {
    if (this.uploading() || this.uploadLocked()) return;
    this.uploading.set(true);
    this.emitBusy();
    // a new receipt makes any earlier draft about a different image: drop it
    // here, and tell the parent to close its review of that stale draft.
    this.draft.set(null);
    this.extractState.set('idle');
    this.extractError.set('');
    this.requesting.set(false);
    // a different image is a different request: the old attempt and any
    // terminal-retry eligibility belong to the image that was replaced
    this.attempt = null;
    this.terminalRetry = false;
    this.terminalRunState = null;
    this.terminalFailureReason = '';
    this.started.emit();
    try {
      this.state.set('uploading');
      this.pct.set(0);
      this.error.set('');
      const res = await this.uploader.reserve('RECEIPT', file.name, file.size);
      if (!res.ok) return this.fail(res.message);
      const put = await this.uploader.put(res.asset.id, file, (p) => this.pct.set(p));
      if (!put.ok) return this.fail(put.message);
      const fin = await this.uploader.finalize(res.asset.id, put.sha256);
      if (!fin.ok) return this.fail(fin.message);
      this.state.set('processing');
      const t = await this.uploader.waitTerminal(res.asset.id);
      if (!this.alive) return;
      if (t.outcome === 'failed') return this.fail(t.errorCode);
      if (t.outcome === 'timeout')
        return this.fail('Processing is taking unusually long — try again.');
      // waitTerminal only reports when the asset leaves the pending set —
      // READY is never in that set, so readiness is confirmed by what uses it:
      // the attach mutation here, and the server's own validation of the
      // extraction request for a staged receipt.
      this.lastAssetId.set(res.asset.id);
      this.selectedName.set(res.asset.displayName || file.name);
      if (this.purchaseId()) {
        // existing purchase: attach immediately, as before
        const attach = await this.gql.mutate(AttachReceiptDocument, {
          input: { purchaseId: this.purchaseId()!, assetId: res.asset.id },
        });
        if (!this.alive) return;
        if (attach.kind !== 'ok')
          return this.fail('Could not reach the server while attaching the receipt.');
        // A payload with neither a receipt nor a problem is not an attach:
        // report it as a failure instead of claiming one.
        const payload = attach.data?.attachReceipt;
        const problems = (payload?.problems ?? []) as MediaProblem[];
        if (!payload?.receipt || problems.length > 0)
          return this.fail(
            problems[0]?.message ?? 'The server did not attach the receipt image. Try again.',
          );
        this.state.set('attached');
        this.attached.emit();
      } else {
        // new purchase: nothing to attach to yet — hand it to the parent,
        // which owns the staged list from here
        this.state.set('ready');
        this.staged.emit({ assetId: res.asset.id, filename: file.name });
      }
      void this.loadCapability();
    } catch {
      // a thrown transport/crypto error must never leave the slot busy forever
      if (this.alive) this.fail('The receipt upload could not be completed.');
    } finally {
      this.uploading.set(false);
      this.emitBusy();
    }
  }

  /** Capability drives the honest affordance: available → offer extraction;
   * unavailable → say why; unknown (query failed) → still offer, the run
   * itself reports PROVIDER_UNAVAILABLE honestly. */
  private async loadCapability(): Promise<void> {
    const res = await this.gql.query(ProviderCapabilitiesDocument, {});
    if (!this.alive) return;
    const c = res.kind === 'ok' ? res.data?.providerCapabilities?.receiptExtraction : null;
    if (c)
      this.cap.set({
        available: c.available,
        unavailableReason: c.unavailableReason,
        externalData: c.externalData,
      });
  }

  /** Optional, advisory OCR review request (T32.b). The shared confirmation
   * calls it on explicit submit; the page's Try extraction again re-arms that
   * confirmation instead of billing a run on a bare click. Nothing here can
   * write to the purchase: the run produces a draft the owner must apply.
   *
   * UI10 (bounded receipt retry): a terminal run (FAILED/STALE/CANCELLED) is
   * final on the server — the same input hash without `forceRerun` replays that
   * dead run forever. So an owner-confirmed retry after an *explicit* terminal
   * run state builds one new attempt (forceRerun + a fresh key) and then keeps
   * exactly that body and key while its outcome is uncertain. Uncertain
   * retries resend the original request instead, which re-uses the active or
   * completed run the server already accepted — no extra billing, no key
   * rotation. Nothing here re-runs on its own. */
  protected async extract(): Promise<void> {
    if (this.disabled() || this.uploading()) return;
    if (!this.lastAssetId()) return;
    // The failed state keeps its error visible, so the run re-arms here rather
    // than silently keeping the action dead. The confirmation is already open
    // when this comes from its own action, and the error stays on screen until
    // this request replaces it.
    if (this.extractState() === 'failed') {
      this.extractState.set('idle');
      this.extractError.set('');
    }
    if (this.extractState() !== 'idle') return;
    const rerun = this.rerunArmed();
    if (rerun) {
      // One rotation per new terminal retry: a fresh semantic attempt, so the
      // server inserts a new run instead of replaying the terminal one.
      this.attempt = {
        input: {
          kind: 'RECEIPT_EXTRACTION',
          receiptAssetId: this.lastAssetId(),
          forceRerun: true,
        },
        clientMutationId: `receipt-ocr-${this.lastAssetId()}-${crypto.randomUUID()}`,
      };
      this.terminalRetry = false;
      this.terminalRunState = null;
      this.terminalFailureReason = '';
    } else if (!this.attempt) {
      // First request for this receipt: the original body and key.
      this.attempt = {
        input: {
          kind: 'RECEIPT_EXTRACTION',
          receiptAssetId: this.lastAssetId(),
        },
        clientMutationId: `receipt-ocr-${this.lastAssetId()}`,
      };
    }
    this.extractState.set('requesting');
    this.extractError.set('');
    this.emitBusy();
    try {
      const res = await this.gql.mutate(RequestAnalysisDocument, {
        input: this.attempt.input,
        clientMutationId: this.attempt.clientMutationId,
      });
      if (!this.alive) return;
      if (res.kind !== 'ok') {
        // Nothing is known about the run: it may already be accepted and
        // active. Keep the attempt and its key so the retry re-uses it, and
        // keep the confirmation mounted with the honest error.
        return this.extractFail('Could not reach the server while starting extraction.');
      }
      const payload = res.data?.requestAnalysis;
      const problems = (payload?.problems ?? []) as RequestProblem[];
      const run = payload?.run ?? null;
      // GraphQL errors are not an accepted request, even if a run is present.
      if (res.errors.length > 0)
        return this.extractFail(res.errors[0]?.message || 'Extraction could not start.');
      if (!payload) return this.extractFail('Extraction could not start.');
      // Same-hash repeat returns the existing run with IDEMPOTENCY_CONFLICT:
      // that is the only problem whose run may be followed — poll what is
      // already in flight. Any other problem is a refusal, run or not, and a
      // list is only reusable when every problem is an idempotency conflict:
      // a conflict bundled with e.g. RATE_LIMITED must be refused, never
      // followed.
      if (problems.length > 0) {
        const refusal = problems.find((p) => p.code !== REUSABLE_PROBLEM);
        if (refusal) return this.extractFail(refusal.message || 'Extraction could not start.');
        if (run === null) return this.extractFail('Extraction could not start.');
      }
      if (!run) return this.extractFail('Extraction could not start.');
      // The request was accepted: the confirmation has done its job. The run's
      // progress and result stay on the page, read-only.
      this.requestEntry()?.markPristine();
      this.requesting.set(false);
      await this.pollRun(run);
    } catch {
      if (this.alive) this.extractFail('Extraction could not start — try again.');
    } finally {
      // every extraction path ends here, accepted or not: the parent's busy
      // state must never stay stuck on a finished or failed run
      this.emitBusy();
    }
  }

  private async pollRun(run: RunView): Promise<void> {
    const deadline = Date.now() + 120_000;
    let view: RunView | null = run;
    while (view && (view.state === 'QUEUED' || view.state === 'RUNNING')) {
      if (!this.alive) return;
      if (Date.now() > deadline)
        return this.extractFail('Extraction is taking unusually long — try again later.');
      this.extractState.set('running');
      this.emitBusy();
      await new Promise((r) => setTimeout(r, 1_500));
      if (!this.alive) return;
      const runId: string = view.id;
      const res: GqlResult<AnalysisRunStatusQuery> = await this.gql.query(
        AnalysisRunStatusDocument,
        { runId },
      );
      if (!this.alive) return;
      view = res.kind === 'ok' ? (res.data?.analysisRun ?? null) : null;
    }
    if (!view) return this.extractFail('Could not reach the server while reading the result.');
    if (view.state === 'SUCCEEDED') {
      const result = view.result;
      if (result?.__typename !== 'ReceiptExtractionResult')
        return this.extractFail('The extraction returned an unexpected result shape.');
      this.extractState.set('draft');
      this.draft.set({
        ...result,
        provider: view.provider,
        model: view.model,
      });
      if (this.alive) this.extracted.emit(this.draft()!);
      return;
    }
    if (view.state === 'FAILED') {
      const why =
        view.failureMessage ??
        (view.failureCode === 'PROVIDER_UNAVAILABLE'
          ? 'This deployment has no receipt extraction engine configured.'
          : 'Extraction failed.');
      // An explicit terminal state: the owner's next confirmed attempt is a
      // new read, because this one can never produce a result.
      this.terminalRetry = true;
      this.terminalRunState = 'FAILED';
      this.terminalFailureReason = why;
      return this.extractFail(why);
    }
    if (view.state === 'STALE') {
      this.terminalRetry = true;
      this.terminalRunState = 'STALE';
      return this.extractFail(
        'The previous read was invalidated before it finished. Retry to read ' +
          'this receipt again.',
      );
    }
    if (view.state === 'CANCELLED') {
      this.terminalRetry = true;
      this.terminalRunState = 'CANCELLED';
      return this.extractFail('The previous read was cancelled. Retry when you are ready.');
    }
    // An unrecognised state says nothing reliable about the run: keep the
    // attempt identity so a retry re-uses it rather than queueing a new run.
    this.extractFail('The extraction run ended in a state this app does not understand.');
  }

  /** Records the failure and keeps the confirmation mounted, so the failure and
   * its retry stay visible and Cancel works again — nothing is in flight. The
   * ending is honest about what does and does not exist: a new purchase is only
   * a draft, and a saved purchase was never touched by extraction. */
  private extractFail(message: string): void {
    this.extractState.set('failed');
    this.extractError.set(
      `${message}${this.purchaseId() ? SAVED_PURCHASE_ENDING : NEW_PURCHASE_ENDING}`,
    );
    this.requesting.set(true);
    this.emitBusy();
  }

  private fail(message: string): void {
    this.state.set('failed');
    this.error.set(message);
  }

  constructor() {
    this.destroyRef.onDestroy(() => {
      this.alive = false;
    });
  }
}

let seq = 0;
