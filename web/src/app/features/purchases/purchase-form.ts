import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  computed,
  inject,
  signal,
  viewChild,
} from "@angular/core";
import {
  FormArray,
  FormControl,
  FormGroup,
  NonNullableFormBuilder,
  ReactiveFormsModule,
} from "@angular/forms";
import { toSignal } from "@angular/core/rxjs-interop";
import { ActivatedRoute, Router } from "@angular/router";
import {
  AttachReceiptDocument,
  CreatePurchaseDocument,
  PlantsDocument,
  PurchaseDetailDocument,
  UpdatePurchaseDocument,
  type CreatePurchaseInput,
  type CreatePurchaseMutation,
  type PlantFieldsFragment,
  type PurchaseAllocationInput,
  type PurchaseFieldsFragment,
  type PurchaseLineInput,
  type UpdatePurchaseMutation,
} from "@canopy/contracts";
import { GraphQLClient } from "../../core/graphql/client";
import {
  ReceiptUpload,
  type ReceiptDraft,
  type StagedReceipt,
} from "../capture/receipt-upload";
import type { MediaProblem } from "../capture/upload";
import { UiEntryDialog } from "../../ui/entry-dialog";
import { UiStatus } from "../../ui/status";
import { UiToasts } from "../../ui/toast";
import {
  blankToNull,
  conflictNote,
  generalProblems,
  problemMap,
  type PayloadProblem,
} from "../collection/shared";
import {
  addMinor,
  cmpMinor,
  fmtDiff,
  fmtMinor,
  parseAmount,
  subMinor,
  toInput,
} from "./money";
import { reviewReceiptAmounts } from "./receipt-review";

type ParseRes = { minor: string; error?: string };

type AllocG = FormGroup<{
  id: FormControl<string>;
  plantId: FormControl<string>;
  line: FormControl<string>;
  tax: FormControl<string>;
  ship: FormControl<string>;
}>;
type LineG = FormGroup<{
  id: FormControl<string>;
  description: FormControl<string>;
  quantity: FormControl<string>;
  total: FormControl<string>;
  allocs: FormArray<AllocG>;
}>;

interface PlantOption {
  id: string;
  name: string;
  /** Acquisition row for this plant, if any. */
  acqId: string | null;
  /** Allocation id that is this plant's recorded price, if any. */
  linkedAllocId: string | null;
}

interface ReconResult {
  ok: boolean;
  notes: string[];
  /** Built mutation input when ok — the preview and the payload are the same numbers. */
  input: CreatePurchaseInput | null;
}

const MAX_LINES = 100; // server command cap
const MAX_ALLOCS = 25; // server validateShape cap per line

/** The honest state of a saved purchase whose staged receipt did not attach:
 * the record exists, the image is still pending, and the next save retries it. */
const RECEIPT_ATTACH_NOTE =
  "Purchase saved, but receipt could not be attached. Save changes to retry.";

/**
 * Purchase editor (T12): header (seller/date/currency/amounts) plus a
 * line/allocation editor. The reconciliation preview mirrors the server's
 * frozen conservation invariants with exact minor-unit arithmetic and
 * quotes exact discrepancies — the server stays authoritative, but a
 * receipt that cannot balance is reported before it is sent. Conflicts
 * never discard the entered draft.
 *
 * The form is modal data entry (UI02): it projects into the shared
 * UiEntryDialog shell, whose header owns the title (h2) — no page h1, no
 * inline card/max-width. Every native control, id, validator and error
 * binding is unchanged. Cancel goes through requestClose(), so an unsaved
 * draft asks Keep editing / Discard; a completed save marks the form
 * pristine and approves the entry before the success navigation, so it
 * never prompts. Receipt extraction follows the same shared entry system:
 * the request is confirmed in a compact dialog (opening it bills nothing)
 * and the resulting draft is reviewed in its own nested shared modal, never
 * as a second inline panel.
 */
@Component({
  selector: "purchase-form",
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, ReceiptUpload, UiEntryDialog, UiStatus],
  template: `
    <!-- Modal data entry: the shared shell wraps EVERY state — load error,
         the voided notice, the form and the receipt cards — so #entry stays
         registered and the portal never disappears mid-navigation
         (reloadLatest). The shell owns the title (h2). -->
    <ui-entry-dialog
      #entry
      [open]="true"
      [title]="editId() ? 'Edit purchase' : 'New purchase'"
      size="wide"
      [busy]="submitting() || receiptBusy()"
      [dirty]="form.dirty || stagedReceipts().length > 0"
      (dismissed)="dismiss()"
    >
      @if (loadError()) {
        <section class="form-section" role="alert">
          <h2>Purchase unavailable</h2>
          <p class="caption">{{ loadError() }}</p>
          <div class="form-actions">
            <button type="button" class="btn btn-primary" (click)="dismiss()">
              Back to ledger
            </button>
          </div>
        </section>
      } @else {
        @if (voided()) {
          <section class="form-section" role="status">
            <p style="margin:0">
              This purchase is voided — it is kept for the record and can no
              longer be edited.
            </p>
          </section>
        }

        @if (conflict()) {
          <section class="form-section" role="alert" aria-labelledby="cf-h">
            <h2 id="cf-h">Not saved</h2>
            <p class="caption" style="margin:0">{{ conflict() }}</p>
            <div class="form-actions">
              <button type="button" class="btn" (click)="reloadLatest()">
                Load the latest version
              </button>
              <span class="caption">Your draft below is preserved.</span>
            </div>
          </section>
        }
        @if (general().length) {
          <section class="form-section" role="alert">
            @for (m of general(); track m) {
              <p class="caption" style="margin:0">{{ m }}</p>
            }
          </section>
        }

        <form [formGroup]="form" (ngSubmit)="submit()">
          <!-- UI09 (receipt first): the receipt slot leads the editor for a
               new purchase as well as an edit, because extraction runs against
               the staged image before any purchase exists. Manual entry stays
               fully available — the receipt is optional either way, and the
               upload affordance disappears only for a voided purchase. -->
          <section class="form-section" aria-labelledby="rc-h">
            <h2 id="rc-h">Receipt</h2>
            @if (!voided()) {
              <receipt-upload
                [purchaseId]="editId()"
                [disabled]="submitting()"
                (busy)="receiptBusy.set($event)"
                (started)="clearExtractionReview()"
                (staged)="onStaged($event)"
                (attached)="refreshReceipts()"
                (extracted)="onExtracted($event)"
              />
            }
            @if (receipts().length) {
              <ul class="receipt-list">
                @for (r of receipts(); track r.id) {
                  <li class="caption">
                    {{ r.asset.displayName ?? "Receipt image" }} —
                    {{ r.asset.state }}
                    @if (r.asset.state === "READY") {
                      ·
                      <a [href]="'/api/media/' + r.asset.id + '/original'"
                        >View original</a
                      >
                    }
                  </li>
                }
              </ul>
            }
            @if (stagedReceipts().length) {
              <ul class="receipt-list">
                @for (s of stagedReceipts(); track s.assetId) {
                  <li class="caption">
                    {{ s.filename }} — attaches when you save the purchase
                  </li>
                }
              </ul>
            }
          </section>

          <section class="form-section" aria-labelledby="pf-h">
            <h2 id="pf-h">Purchase details</h2>
            <p class="field-hint">
              Or enter the receipt by hand — every field stays editable after
              you apply extracted details.
            </p>
            <div class="form-grid receipt-grid">
              <div>
                <label class="field-label" for="pf-seller"
                  >Seller (optional)</label
                >
                <input
                  id="pf-seller"
                  class="input"
                  formControlName="headSeller"
                  maxlength="200"
                  autocomplete="off"
                />
              </div>
              <div>
                <label class="field-label" for="pf-date"
                  >Purchased on (optional)</label
                >
                <input
                  id="pf-date"
                  class="input"
                  type="date"
                  formControlName="headDate"
                />
              </div>
              <div>
                <label class="field-label" for="pf-cur">Currency</label>
                <input
                  id="pf-cur"
                  class="input"
                  formControlName="currency"
                  maxlength="3"
                  placeholder="USD"
                  (blur)="normalizeCurrency()"
                />
              </div>
              <div>
                <label class="field-label" for="pf-total">Total paid</label>
                <input
                  id="pf-total"
                  class="input"
                  formControlName="total"
                  inputmode="decimal"
                  autocomplete="off"
                />
              </div>
              <div>
                <label class="field-label" for="pf-tax">Tax (optional)</label>
                <input
                  id="pf-tax"
                  class="input"
                  formControlName="tax"
                  inputmode="decimal"
                  autocomplete="off"
                />
              </div>
              <div>
                <label class="field-label" for="pf-ship"
                  >Shipping (optional)</label
                >
                <input
                  id="pf-ship"
                  class="input"
                  formControlName="shipping"
                  inputmode="decimal"
                  autocomplete="off"
                />
              </div>
            </div>
          </section>

          <div
            class="line-items"
            formArrayName="lines"
            role="group"
            aria-label="Line items"
          >
            @for (ln of lines.controls; track ln; let li = $index) {
              <section class="form-section line-card" [formGroup]="ln">
                <div class="row spread line-head">
                  <h2 style="margin:0">Line {{ li + 1 }}</h2>
                  @if (lines.length > 1) {
                    <button type="button" class="btn" (click)="removeLine(li)">
                      Remove line
                    </button>
                  }
                </div>
                <div class="form-grid line-grid">
                  <div class="line-desc">
                    <label class="field-label" [for]="'l' + li + '-d'"
                      >Description</label
                    >
                    <input
                      [id]="'l' + li + '-d'"
                      class="input"
                      formControlName="description"
                      maxlength="200"
                    />
                  </div>
                  <div>
                    <label class="field-label" [for]="'l' + li + '-q'"
                      >Quantity</label
                    >
                    <input
                      [id]="'l' + li + '-q'"
                      class="input"
                      formControlName="quantity"
                      inputmode="decimal"
                    />
                  </div>
                  <div>
                    <label class="field-label" [for]="'l' + li + '-t'"
                      >Line total</label
                    >
                    <input
                      [id]="'l' + li + '-t'"
                      class="input"
                      formControlName="total"
                      inputmode="decimal"
                      autocomplete="off"
                    />
                  </div>
                </div>

                @for (
                  al of ln.controls.allocs.controls;
                  track al;
                  let ai = $index
                ) {
                  <div
                    class="alloc-row"
                    [class.alloc-row--overhead]="hasOverhead()"
                    [formGroup]="al"
                  >
                    <div class="alloc-plant">
                      <label
                        class="field-label"
                        [for]="'l' + li + '-a' + ai + '-p'"
                        >Plant</label
                      >
                      <select
                        [id]="'l' + li + '-a' + ai + '-p'"
                        class="input"
                        formControlName="plantId"
                      >
                        <option value="">Unallocated supplies</option>
                        @for (p of plants(); track p.id) {
                          <option [value]="p.id">{{ p.name }}</option>
                        }
                      </select>
                      @if (linkedName(al); as name) {
                        <p class="field-hint" style="margin:4px 0 0">
                          Recorded price of {{ name }} — removing it reverts
                          that plant to cost unknown.
                        </p>
                      }
                    </div>
                    <div class="alloc-amt">
                      <label
                        class="field-label"
                        [for]="'l' + li + '-a' + ai + '-l'"
                        >Item</label
                      >
                      <input
                        [id]="'l' + li + '-a' + ai + '-l'"
                        class="input"
                        formControlName="line"
                        inputmode="decimal"
                        autocomplete="off"
                      />
                    </div>
                    @if (hasOverhead()) {
                      <div class="alloc-amt">
                        <label
                          class="field-label"
                          [for]="'l' + li + '-a' + ai + '-t'"
                          >Tax</label
                        >
                        <input
                          [id]="'l' + li + '-a' + ai + '-t'"
                          class="input"
                          formControlName="tax"
                          inputmode="decimal"
                          autocomplete="off"
                        />
                      </div>
                      <div class="alloc-amt">
                        <label
                          class="field-label"
                          [for]="'l' + li + '-a' + ai + '-s'"
                          >Shipping</label
                        >
                        <input
                          [id]="'l' + li + '-a' + ai + '-s'"
                          class="input"
                          formControlName="ship"
                          inputmode="decimal"
                          autocomplete="off"
                        />
                      </div>
                    }
                    @if (ln.controls.allocs.length > 1) {
                      <button
                        type="button"
                        class="btn"
                        (click)="ln.controls.allocs.removeAt(ai)"
                      >
                        Remove
                      </button>
                    }
                  </div>
                }
                @if (ln.controls.allocs.length < MAX_ALLOCS) {
                  <button type="button" class="btn" (click)="addAlloc(ln)">
                    Add allocation
                  </button>
                }
              </section>
            }
          </div>

          @if (lines.length < MAX_LINES) {
            <div class="form-actions">
              <button type="button" class="btn" (click)="addLine()">
                Add line
              </button>
            </div>
          }

          <section class="recon" aria-live="polite" aria-label="Reconciliation">
            @if (recon(); as r) {
              @if (r.ok && r.input) {
                <p class="recon-ok">
                  Balanced: {{ fmt(r.input.totalMinor, r.input.currency) }}
                </p>
              } @else if (attempted()) {
                <ul class="recon-notes">
                  @for (n of r.notes; track $index) {
                    <li>{{ n }}</li>
                  }
                </ul>
              }
            }
            @if (pendingUnlinks().length) {
              <div class="row" style="margin-top:var(--sp-3)">
                <input
                  id="pf-unlink"
                  type="checkbox"
                  [checked]="unlinkConfirmed()"
                  (change)="unlinkConfirmed.set($any($event.target).checked)"
                />
                <label class="field-label" for="pf-unlink" style="margin:0">
                  Confirm: remove the recorded price for {{ unlinkNames() }} —
                  {{ unlinkNames() }} revert{{
                    pendingUnlinks().length === 1 ? "s" : ""
                  }}
                  to cost unknown.
                </label>
              </div>
              @if (unlinkWarn()) {
                <p class="field-error" style="margin:6px 0 0">
                  Tick the confirmation to remove a recorded price.
                </p>
              }
            }
          </section>

          @if (!voided()) {
            <div class="form-actions">
              <button type="button" class="btn" (click)="entry.requestClose()">
                Cancel
              </button>
              <button
                type="submit"
                class="btn btn-primary"
                [disabled]="
                  submitting() ||
                  (pendingUnlinks().length > 0 && !unlinkConfirmed())
                "
              >
                {{
                  submitting()
                    ? "Saving…"
                    : editId()
                      ? "Save changes"
                      : "Add purchase"
                }}
              </button>
            </div>
          }
        </form>
      }
    </ui-entry-dialog>

    <!-- Extracted-details review: the same shared foundation, nested inside the
         one purchase dialog. It carries the actual extracted values, the
         provider/model provenance, the “— not read” markers for facts the run
         could not read, and the amount check computed here from the draft's
         own numbers (reviewAmounts), not the draft's needsReconciliation
         flag. X/Cancel closes it
         while the draft stays available, so Review extracted details reopens it
         without another billed run. Apply calls the same money-safe applyDraft();
         Discard clears the draft as if it had never been read. -->
    <ui-entry-dialog
      #reviewEntry
      [open]="reviewing()"
      title="Extracted from receipt"
      description="Review the details before applying them to your purchase"
      size="regular"
      (dismissed)="closeReview()"
    >
      @if (ocrDraft(); as d) {
        @let amounts = reviewAmounts(d);
        <div class="entry-form">
          <div class="form-section">
            <p class="field-hint">
              Review every value, then apply what is right. Applying fills the
              purchase form — it never saves the purchase.
            </p>
            @if (reviewError(); as msg) {
              <p class="field-error" role="alert">{{ msg }}</p>
            }
            @switch (amounts.state) {
              @case ("balanced") {
                <p>
                  <ui-status kind="ok" label="Read amounts balance" />
                </p>
                <p class="field-hint">
                  The read item amounts and any read tax/shipping add up to the
                  receipt total. Review the date, names and currency before
                  applying.
                </p>
              }
              @case ("mismatch") {
                <p>
                  <ui-status kind="warn" label="Amounts need checking" />
                </p>
                <p class="field-hint">
                  The read amounts ({{
                    fmt(amounts.readMinor, receiptCurrency(d))
                  }}) do not yet add up to the receipt total ({{
                    fmt(d.totalMinor!, receiptCurrency(d))
                  }}).
                </p>
              }
              @case ("incomplete") {
                <p>
                  <ui-status kind="warn" label="Amounts incomplete" />
                </p>
                <p class="field-hint">
                  Some amounts were not read, so they could not be checked
                  against the total. Check the receipt.
                </p>
              }
            }
          </div>
          <div class="form-section">
            <h3>Extracted values</h3>
            <dl class="ocr-grid">
              <dt>Seller</dt>
              <dd>{{ d.seller ?? "— not read" }}</dd>
              <dt>Purchased on</dt>
              <dd>{{ d.purchasedOn ?? "— not read" }}</dd>
              <dt>Currency</dt>
              <dd>
                {{ d.currency ?? "Not identified" }}
                @if (!d.currency) {
                  <p class="field-hint">
                    Currency was not identified. Amounts are shown in
                    {{ receiptCurrency(d) }} selected for this purchase.
                  </p>
                }
              </dd>
              <dt>Total paid</dt>
              <dd>
                {{
                  d.totalMinor != null
                    ? fmt(d.totalMinor, receiptCurrency(d))
                    : "— not read"
                }}
              </dd>
              <dt>Tax</dt>
              <dd>
                {{
                  d.taxMinor != null
                    ? fmt(d.taxMinor, receiptCurrency(d))
                    : "— not read"
                }}
              </dd>
              <dt>Shipping</dt>
              <dd>
                {{
                  d.shippingMinor != null
                    ? fmt(d.shippingMinor, receiptCurrency(d))
                    : "— not read"
                }}
              </dd>
            </dl>
            @if (d.lines.length) {
              <ul class="caption ocr-list">
                @for (ln of d.lines; track $index) {
                  <li>
                    {{ ln.description }} —
                    {{
                      ln.amountMinor != null
                        ? fmt(ln.amountMinor, receiptCurrency(d))
                        : "amount not legible"
                    }}
                  </li>
                }
              </ul>
            } @else {
              <p class="field-hint">
                No line items were read — enter them from the receipt if needed.
              </p>
            }
          </div>
          <div class="form-section">
            <h3>How this was read</h3>
            <p class="field-hint">
              Engine: {{ d.provider ?? "local"
              }}{{ d.model ? " · " + d.model : "" }}
            </p>
          </div>
          <div class="form-actions">
            <button type="button" class="btn" (click)="closeReview()">
              Cancel
            </button>
            <button type="button" class="btn" (click)="discardDraft()">
              Discard draft
            </button>
            <button
              type="button"
              class="btn btn-primary"
              (click)="applyDraft()"
            >
              Apply extracted details
            </button>
          </div>
        </div>
      }
    </ui-entry-dialog>
  `,
  styles: [
    `
      /* UI07: dense financial form. The shared .entry-form/.form-section/
         .form-grid/.form-actions classes live in styles/base.css and reach
         the projected content through the dialog shell; this block tightens
         the vertical rhythm for the purchase editor and lays out the
         receipt / line / allocation grids so a typical one-line receipt fits
         the modal without scrolling. */
      form {
        gap: var(--sp-4);
      }
      .form-section h2 {
        font-size: 15px;
        line-height: 22px;
        font-weight: 650;
        margin: 0;
      }
      /* one row for the whole receipt header on desktop. Labels wrap to two
         lines for the narrower columns ("Purchased on", "Shipping"), so the
         fields bottom-align: the inputs share a baseline even when their
         headers do not. */
      .receipt-grid {
        grid-template-columns:
          minmax(0, 1.6fr) minmax(0, 1.2fr) minmax(0, 0.6fr) minmax(0, 1fr)
          minmax(0, 0.8fr) minmax(0, 1fr);
        align-items: end;
      }
      /* UI14: compact line rows. The lines wrapper is a 8px grid so cards
         stack tightly; card padding/gaps/radius are scoped through the
         shared .entry-form shell so the receipt header and the review
         modal keep their own rhythm. */
      .line-items {
        display: grid;
        gap: 8px;
      }
      .line-card.form-section {
        border: 1px solid var(--border-decorative);
        border-radius: 10px;
        padding: 10px 12px;
        gap: 8px;
      }
      .line-card .line-head {
        align-items: center;
      }
      .line-card .line-head h2 {
        font-size: 12px;
        line-height: 18px;
      }
      /* compact secondary actions inside a line card: Remove line / Add
         allocation / Remove allocation stay inline and slim on desktop;
         justify-self:start keeps Add allocation from stretching full
         width. Focus treatment stays the shared global one. */
      .line-card .btn {
        min-height: 28px;
        height: 28px;
        padding: 3px 8px;
        font-size: 12px;
        line-height: 18px;
        justify-self: start;
      }
      .line-grid {
        grid-template-columns: minmax(0, 1fr) 72px 112px;
        gap: 8px;
      }
      /* desktop line-card controls: 36px fields with a tighter inner
         rhythm. The select rule is written explicitly so it outranks the
         global select.input:not([multiple]) treatment; the painted arrow
         and its right padding stay from select.css. */
      .line-card .input {
        min-height: 36px;
        height: 36px;
        padding: 6px 10px;
        font-size: 14px;
        line-height: 20px;
      }
      .line-card select.input:not([multiple]) {
        min-height: 36px;
        height: 36px;
        padding: 6px 36px 6px 10px;
        font-size: 14px;
        line-height: 20px;
      }
      .line-card .form-grid > *:not(.field) .field-label,
      .line-card .alloc-row .field-label {
        margin: 0 0 4px;
        font-size: 12px;
        line-height: 16px;
      }
      .alloc-row {
        display: grid;
        grid-template-columns: minmax(0, 1fr) 112px auto;
        gap: 8px;
        align-items: end;
        border-top: 1px solid var(--border-decorative);
        padding-top: 8px;
        margin-top: 0;
      }
      /* per-allocation tax/shipping only appear when the receipt carries
         overhead at all — the header amounts auto-split across allocations */
      .alloc-row--overhead {
        grid-template-columns: minmax(0, 1fr) repeat(
            3,
            minmax(64px, 88px)
          ) auto;
      }
      /* numeric columns must be able to shrink instead of overflowing */
      .alloc-row > *,
      .line-grid > *,
      .line-card .input {
        min-width: 0;
      }
      .recon {
        display: grid;
        gap: var(--sp-2);
      }
      .receipt-list {
        list-style: none;
        margin: var(--sp-2) 0 0;
        padding: 0;
        /* a camera filename like IMG_20261103_142359(1).jpg has no break
           opportunity, so it must be allowed to wrap inside the phone modal */
        overflow-wrap: anywhere;
      }
      .recon-ok {
        margin: 0;
        font-size: 14px;
        font-weight: 600;
        color: var(--accent);
      }
      .recon-notes {
        margin: 0;
        padding-left: 1.2em;
      }
      .recon-notes li {
        font-size: 14px;
        margin-bottom: 4px;
      }
      .ocr-grid {
        display: grid;
        grid-template-columns: auto 1fr;
        gap: var(--sp-1) var(--sp-4);
        margin: 0;
      }
      .ocr-grid dt {
        font-size: var(--fs-caption);
        color: var(--text-muted);
      }
      .ocr-grid dd {
        margin: 0;
        font-weight: 600;
      }
      /* the currency clarification belongs to its own dd, so it stays in the
         description column without an empty dt spacer; it drops back to the
         normal caption weight and keeps the section's 24px rhythm */
      .ocr-grid dd .field-hint {
        margin: 2px 0 0;
        font-weight: 400;
      }
      /* the extracted-lines list sits in the review modal's 24px section
         rhythm, so it carries no extra margin of its own */
      .ocr-list {
        margin: 0;
        padding-left: 1.2em;
      }
      @media (max-width: 900px) {
        .receipt-grid {
          grid-template-columns: repeat(3, minmax(0, 1fr));
        }
      }
      @media (max-width: 767px) {
        .receipt-grid {
          grid-template-columns: repeat(2, minmax(0, 1fr));
        }
        .line-grid {
          grid-template-columns: 1fr 1fr;
        }
        /* description takes the whole row, quantity | total share 50/50 */
        .line-grid .line-desc {
          grid-column: 1 / -1;
        }
        .alloc-row,
        .alloc-row--overhead {
          grid-template-columns: 1fr 1fr;
        }
        .alloc-plant {
          grid-column: 1 / -1;
        }
        .alloc-row > .btn {
          grid-column: 1 / -1;
          justify-self: end;
        }
      }
      @media (min-width: 360px) and (max-width: 767px) {
        .alloc-row--overhead {
          grid-template-columns: repeat(3, minmax(0, 1fr));
        }
      }
      /* touch environments (phones / coarse pointers): back to the shared
         44px targets and 16px field text so iOS never zooms on focus */
      @media (pointer: coarse), (max-width: 767px) {
        .line-card .btn {
          min-height: var(--touch-min);
          height: auto;
          padding: 0 var(--sp-4);
          font-size: var(--fs-small);
        }
        .line-card .input {
          min-height: var(--touch-min);
          height: auto;
          padding: var(--sp-2) var(--sp-3);
          font-size: var(--fs-body);
        }
        .line-card select.input:not([multiple]) {
          min-height: var(--touch-min);
          height: auto;
          padding: var(--sp-2) var(--sp-8) var(--sp-2) var(--sp-3);
          font-size: var(--fs-body);
        }
      }
    `,
  ],
})
export class PurchaseForm implements OnDestroy {
  private readonly gql = inject(GraphQLClient);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly toasts = inject(UiToasts);
  private readonly fb = inject(NonNullableFormBuilder);
  private readonly abort = new AbortController();

  protected readonly editId = signal<string | null>(null);
  protected readonly plants = signal<PlantOption[]>([]);
  protected readonly receipts = signal<PurchaseFieldsFragment["receipts"]>([]);
  protected readonly revision = signal<number | null>(null);
  protected readonly voided = signal(false);
  protected readonly loadError = signal<string | null>(null);
  protected readonly conflict = signal<string | null>(null);
  protected readonly general = signal<string[]>([]);
  protected readonly submitting = signal(false);
  protected readonly unlinkConfirmed = signal(false);
  protected readonly unlinkWarn = signal(false);
  /** Advisory OCR draft (T32.b): reviewed in its own shared modal; only
   * applyDraft() ever touches the form, and the owner still saves through the
   * normal flow. The draft outlives the review dialog, so it can be reopened
   * from the receipt slot without another billed run. */
  protected readonly ocrDraft = signal<ReceiptDraft | null>(null);
  /** declarative [open] of the extracted-details review modal */
  protected readonly reviewing = signal(false);
  /** why applying was refused, shown inside the review modal itself: the draft
   * stays available, and the reason is never left behind the open dialog. */
  protected readonly reviewError = signal<string | null>(null);
  /** the receipt slot's own upload/extraction pass — folded into the shell's
   * busy state so a save can never race an upload, and so the save button
   * cannot be pressed while bytes are still moving. */
  protected readonly receiptBusy = signal(false);
  /** READY receipts the editor owns but cannot attach yet (UI09: a new
   * purchase has no id to attach to). The parent owns this list: the slot
   * emits once per finished upload, dedup keeps a re-pick honest, and the
   * entries leave only when the attach really succeeded. */
  protected readonly stagedReceipts = signal<StagedReceipt[]>([]);
  /** the owner has pressed Save: reconciliation notes then become actionable
   * feedback instead of noise on a form nobody has filled in yet. */
  protected readonly attempted = signal(false);
  /** stable create idempotency key for the current payload: an uncertain
   * network result retries under the same uuid, so the server dedupes instead
   * of creating a second purchase. Rotates only when the payload changes. */
  private createKey: { payload: string; key: string } | null = null;
  protected readonly MAX_LINES = MAX_LINES;
  protected readonly MAX_ALLOCS = MAX_ALLOCS;

  /** The modal host: Cancel routes through requestClose() (dirty-guarded), and
   * a completed save calls markPristine() so the success navigation is never
   * blocked by the entry guard. The shell wraps every state, so it always
   * renders; optional only to keep the lookup defensive. */
  private readonly entry = viewChild<UiEntryDialog>("entry");

  /** The nested review modal. It holds no inputs of its own — the only
   * controls are Discard/Apply — so closing it never needs a discard prompt
   * beyond the one this component drives explicitly. */
  private readonly reviewEntry = viewChild<UiEntryDialog>("reviewEntry");

  protected readonly headSeller = this.fb.control("");
  protected readonly headDate = this.fb.control("");
  protected readonly currency = this.fb.control("USD");
  protected readonly total = this.fb.control("");
  protected readonly tax = this.fb.control("");
  protected readonly shipping = this.fb.control("");
  protected readonly lines = this.fb.array<LineG>([]);

  protected readonly form = this.fb.group({
    headSeller: this.headSeller,
    headDate: this.headDate,
    currency: this.currency,
    total: this.total,
    tax: this.tax,
    shipping: this.shipping,
    lines: this.lines,
  });

  /** Any draft edit re-runs the reconciliation preview. */
  private readonly draft = toSignal(this.form.valueChanges, {
    initialValue: undefined,
  });

  // ── amount cascades (UI07) ──────────────────────────────────────────────
  // The conservation invariants tie total, line totals and allocation items
  // together; making the owner balance three inputs by hand is how a simple
  // price correction turns into an off-by error. Instead the form follows
  // intent: editing an item moves its line's total (when it is the line's
  // only allocation), editing a line total moves the receipt total, and
  // editing the total pushes down onto a lone line. The reconciliation
  // preview still validates every number exactly — cascades only remove
  // the mechanical bookkeeping, never the checks.

  /** true while prefilling from the server: cascades must not rewrite the
   * loaded (already-balanced) values during the final value pass. */
  private hydrating = false;
  /** re-entrancy guard for cascade-driven setValue calls. */
  private syncing = false;
  /** which amount the owner last typed — decides overhead edit direction. */
  private lastAmountTouched: "total" | "overhead" | "lines" | null = null;

  /** per-allocation tax/shipping inputs only earn their space when the
   * receipt actually carries overhead. */
  protected readonly hasOverhead = computed(() => {
    this.draft();
    const cur = (this.currency.value || "USD").trim().toUpperCase();
    const t = parseAmount(this.tax.value ?? "", cur, { required: false });
    const s = parseAmount(this.shipping.value ?? "", cur, { required: false });
    return (!t.error && t.minor !== "0") || (!s.error && s.minor !== "0");
  });

  constructor() {
    this.addLine();
    this.total.valueChanges.subscribe(() => this.onTotalEdit());
    this.tax.valueChanges.subscribe(() => this.onOverheadEdit());
    this.shipping.valueChanges.subscribe(() => this.onOverheadEdit());
    const id = this.route.snapshot.paramMap.get("id");
    void this.loadPlants();
    if (id) {
      this.editId.set(id);
      void this.loadPurchase(id);
    }
  }

  ngOnDestroy(): void {
    this.abort.abort();
  }

  /** Modal dismissed (Cancel, X, Escape, backdrop) — the ledger is the safe
   * parent for both create and edit routes. */
  protected dismiss(): void {
    void this.router.navigate(["/purchases"]);
  }

  protected fmt(minor: string, currency: string): string {
    return fmtMinor(minor, currency);
  }

  /** Amount arithmetic for the extracted-details review. Deliberately
   * independent of the draft's needsReconciliation flag (see receipt-review). */
  protected readonly reviewAmounts = reviewReceiptAmounts;

  /** Currency the review template formats amounts in. When the run did not
   * read a currency, amounts are shown in the currency selected for this
   * purchase — the draft is never rewritten with an inferred currency. */
  protected receiptCurrency(d: ReceiptDraft): string {
    return (d.currency || this.currency.value || "USD").trim().toUpperCase();
  }

  protected normalizeCurrency(): void {
    this.currency.setValue((this.currency.value || "").trim().toUpperCase());
  }

  protected addLine(): void {
    const ln = this.fb.group({
      id: this.fb.control(""),
      description: this.fb.control(""),
      quantity: this.fb.control("1"),
      total: this.fb.control(""),
      allocs: this.fb.array<AllocG>([this.newAlloc()]),
    });
    this.lines.push(ln);
    this.wireLine(ln);
  }

  protected removeLine(i: number): void {
    this.lines.removeAt(i);
  }

  protected addAlloc(ln: LineG): void {
    const al = this.newAlloc();
    ln.controls.allocs.push(al);
    this.wireAlloc(ln, al);
  }

  private newAlloc(): AllocG {
    return this.fb.group({
      id: this.fb.control(""),
      plantId: this.fb.control(""),
      line: this.fb.control(""),
      tax: this.fb.control(""),
      ship: this.fb.control(""),
    });
  }

  /** subscribe one line group's amount controls to the cascades (prefill
   * pushes lines directly, so wiring lives here, not in addLine alone). */
  private wireLine(ln: LineG): void {
    ln.controls.total.valueChanges.subscribe(() => this.onLineTotalEdit(ln));
    for (const al of ln.controls.allocs.controls) this.wireAlloc(ln, al);
  }

  private wireAlloc(ln: LineG, al: AllocG): void {
    al.controls.line.valueChanges.subscribe(() => this.onAllocItemEdit(ln, al));
  }

  // ── cascade handlers ────────────────────────────────────────────────────

  /** Owner typed the receipt total: with a single line, push the net
   * (total − tax − shipping) down onto it and its sole allocation. */
  private onTotalEdit(): void {
    if (this.hydrating || this.syncing) return;
    this.lastAmountTouched = "total";
    if (this.lines.length !== 1) return;
    const net = this.netOfTotal();
    if (net === null) return;
    this.syncing = true;
    this.setLineAndSoleAlloc(this.lines.at(0)!, net);
    this.syncing = false;
  }

  /** Owner typed tax/shipping. If the total was the last thing typed the
   * intent is "this receipt is X all-in" → push the new net down; otherwise
   * the lines are the source of truth → the total grows to cover overhead.
   * Either way the header overhead then splits across allocations by item
   * weight (largest remainder, exact minor units). */
  private onOverheadEdit(): void {
    if (this.hydrating || this.syncing) return;
    const prev = this.lastAmountTouched;
    this.lastAmountTouched = "overhead";
    this.syncing = true;
    if (prev === "total" && this.lines.length === 1) {
      const net = this.netOfTotal();
      if (net !== null) this.setLineAndSoleAlloc(this.lines.at(0)!, net);
    } else {
      this.pushUpTotal();
    }
    this.distributeOverhead();
    this.syncing = false;
  }

  /** Owner typed a line total: its sole allocation follows, and the
   * receipt total re-derives from the lines. */
  private onLineTotalEdit(ln: LineG): void {
    if (this.hydrating || this.syncing) return;
    this.lastAmountTouched = "lines";
    this.syncing = true;
    if (ln.controls.allocs.length === 1) {
      const v = ln.controls.total.value ?? "";
      ln.controls.allocs.at(0)!.controls.line.setValue(v);
    }
    this.pushUpTotal();
    this.syncing = false;
  }

  /** Owner typed an item amount: on a single-allocation line the line
   * total follows (this is the "the price was actually 240" case), and the
   * receipt total re-derives. Multi-allocation lines keep their total —
   * the reconciliation notes guide the split. */
  private onAllocItemEdit(ln: LineG, al: AllocG): void {
    if (this.hydrating || this.syncing) return;
    this.lastAmountTouched = "lines";
    this.syncing = true;
    if (ln.controls.allocs.length === 1) {
      ln.controls.total.setValue(al.controls.line.value ?? "");
    }
    this.pushUpTotal();
    this.syncing = false;
  }

  // ── cascade helpers (exact minor units, never touch invalid inputs) ─────

  private currencyCode(): string {
    return (this.currency.value || "USD").trim().toUpperCase();
  }

  /** total − tax − shipping, or null when any amount is invalid/negative. */
  private netOfTotal(): string | null {
    const cur = this.currencyCode();
    const t = parseAmount(this.total.value ?? "", cur, { required: true });
    const tax = parseAmount(this.tax.value ?? "", cur, { required: false });
    const ship = parseAmount(this.shipping.value ?? "", cur, {
      required: false,
    });
    if (t.error || tax.error || ship.error) return null;
    const net = subMinor(t.minor, addMinor(tax.minor, ship.minor));
    return net.startsWith("-") ? null : net;
  }

  /** receipt total = Σ line totals + tax + shipping, when every part parses. */
  private pushUpTotal(): void {
    const cur = this.currencyCode();
    const tax = parseAmount(this.tax.value ?? "", cur, { required: false });
    const ship = parseAmount(this.shipping.value ?? "", cur, {
      required: false,
    });
    if (tax.error || ship.error) return;
    let sum = addMinor(tax.minor, ship.minor);
    for (const ln of this.lines.controls) {
      const l = parseAmount(ln.controls.total.value ?? "", cur, {
        required: true,
      });
      if (l.error) return;
      sum = addMinor(sum, l.minor);
    }
    this.total.setValue(toInput(sum, cur));
  }

  /** set a line's total, and its sole allocation's item, to the same
   * exact amount (formatted for the inputs). */
  private setLineAndSoleAlloc(ln: LineG, minor: string): void {
    const cur = this.currencyCode();
    const v = toInput(minor, cur);
    ln.controls.total.setValue(v);
    if (ln.controls.allocs.length === 1)
      ln.controls.allocs.at(0)!.controls.line.setValue(v);
  }

  /** split header tax/shipping across every allocation proportionally to
   * item amounts (equal split when nothing is itemized yet), largest
   * remainder — the sums land exactly on the header amounts. */
  private distributeOverhead(): void {
    const cur = this.currencyCode();
    const allocs = this.lines.controls.flatMap(
      (ln) => ln.controls.allocs.controls,
    );
    if (!allocs.length) return;
    const tax = parseAmount(this.tax.value ?? "", cur, { required: false });
    const ship = parseAmount(this.shipping.value ?? "", cur, {
      required: false,
    });
    if (!tax.error) this.spreadOverhead(tax.minor, allocs, "tax", cur);
    if (!ship.error) this.spreadOverhead(ship.minor, allocs, "ship", cur);
  }

  private spreadOverhead(
    totalMinor: string,
    allocs: AllocG[],
    key: "tax" | "ship",
    cur: string,
  ): void {
    const weights = allocs.map((al) => {
      const p = parseAmount(al.controls.line.value ?? "", cur, {
        required: false,
      });
      return p.error ? "0" : p.minor;
    });
    let weightSum = "0";
    for (const w of weights) weightSum = addMinor(weightSum, w);
    if (weightSum === "0") {
      weights.fill("1");
      weightSum = String(allocs.length);
    }
    const shares: string[] = [];
    let assigned = "0";
    for (let i = 0; i < allocs.length; i++) {
      // floor(total * weight / weightSum) in exact BigInt arithmetic
      const w = BigInt(weights[i]);
      const t = BigInt(totalMinor);
      const ws = BigInt(weightSum);
      const share = ((t * w) / ws).toString();
      shares.push(share);
      assigned = addMinor(assigned, share);
    }
    // largest remainder gets the leftover cents so the split is exact
    let leftover = subMinor(totalMinor, assigned);
    if (leftover !== "0") {
      const order = shares
        .map((s, i) => ({
          i,
          frac: (BigInt(totalMinor) * BigInt(weights[i])) % BigInt(weightSum),
        }))
        .sort((a, b) => (a.frac < b.frac ? 1 : a.frac > b.frac ? -1 : 0));
      for (const { i } of order) {
        if (leftover === "0") break;
        shares[i] = addMinor(shares[i], "1");
        leftover = subMinor(leftover, "1");
      }
    }
    allocs.forEach((al, i) =>
      al.controls[key].setValue(toInput(shares[i], cur)),
    );
  }

  // ── loading ─────────────────────────────────────────────────────────────

  private async loadPlants(): Promise<void> {
    const res = await this.gql.query(
      PlantsDocument,
      {
        first: 100,
        filter: { status: ["ACTIVE", "DEAD", "ARCHIVED"] },
        order: "NAME_ASC",
      },
      this.abort.signal,
    );
    if (res.kind !== "ok" || !res.data) return;
    this.plants.set(
      res.data.plants.items.map((p: PlantFieldsFragment) => ({
        id: p.id,
        name: p.name,
        acqId: p.acquisition?.id ?? null,
        linkedAllocId: p.acquisition?.initialPurchaseAllocation?.id ?? null,
      })),
    );
  }

  protected async reloadLatest(): Promise<void> {
    const id = this.editId();
    if (!id) return;
    this.conflict.set(null);
    await this.loadPurchase(id);
    // A successful read re-syncs the form with the server, so the draft is no
    // longer at risk — the modal stops prompting on the next dismissal. A
    // failed read leaves the draft (and the prompt) exactly as it was.
    if (this.loaded()) {
      this.form.markAsPristine();
      this.entry()?.markPristine();
    }
  }

  /** A receipt was attached: refresh the receipt list only — an in-progress
   * draft in the form above is never overwritten by someone else's write. */
  protected async refreshReceipts(): Promise<void> {
    const id = this.editId();
    if (!id) return;
    const res = await this.gql.query(
      PurchaseDetailDocument,
      { id },
      this.abort.signal,
    );
    if (res.kind === "ok" && res.data?.purchase)
      this.receipts.set(res.data.purchase.receipts);
  }

  /** A READY receipt the slot finished uploading. The editor owns the list,
   * so a re-pick of the same asset cannot stage a duplicate attach. Staging is
   * a real pending change: the entry stays dirty-guarded until it is attached. */
  protected onStaged(s: StagedReceipt): void {
    this.stagedReceipts.update((cur) =>
      cur.some((r) => r.assetId === s.assetId) ? cur : [...cur, s],
    );
    this.form.markAsDirty();
    this.entry()?.markDirty();
  }

  /** A new upload started: any draft on screen describes a different image,
   * so its review is closed and dropped rather than left to be applied to the
   * wrong receipt. */
  protected clearExtractionReview(): void {
    this.ocrDraft.set(null);
    this.reviewError.set(null);
    this.reviewEntry()?.markPristine();
    this.reviewing.set(false);
  }

  /** A finished run hands the draft over — and so does the receipt slot's
   * Review action, which replays the same cached draft rather than requesting
   * another run. The draft is kept, and its review opens as its own shared
   * modal on top of the one purchase dialog. Nothing is written to the form by
   * opening it, so review stays available even after a prior Apply/Discard. */
  protected onExtracted(d: ReceiptDraft): void {
    this.ocrDraft.set(d);
    this.reviewError.set(null);
    this.reviewing.set(true);
  }

  /** X / Escape / backdrop / Cancel: the draft stays available, so the owner
   * can review it again or apply it later without another billed run. */
  protected closeReview(): void {
    this.reviewEntry()?.markPristine();
    this.reviewing.set(false);
  }

  /** Explicit: the draft is dropped as if it had never been read. Nothing in
   * the purchase form is touched, so the draft's own dirty state is unchanged. */
  protected discardDraft(): void {
    this.ocrDraft.set(null);
    this.reviewEntry()?.markPristine();
    this.reviewing.set(false);
    this.toasts.ok(
      "Extraction draft discarded — the purchase form is unchanged.",
    );
  }

  /** Hydrate the reviewed draft into the form — exact minor-unit values, no
   * conversion, no derived totals. Rules the owner can rely on:
   * - a draft too large to fit the server's line cap is refused and stays for
   *   review instead of being truncated;
   * - extracted lines replace lines only when the editor holds nothing but the
   *   blank initial line; owner-typed lines and allocations are never clobbered;
   * - every extracted line becomes one allocation whose item amount matches
   *   its amount, blank when the amount was not legible;
   * - an unread total stays blank — a sum of partial lines is not a total the
   *   receipt confirmed;
   * - a currency the owner already chose is never converted to the draft's.
   * - a header field the owner has typed into keeps the owner's value; only
   *   pristine controls (including the server-prefilled ones on an edit) take
   *   the draft's readable value.
   * The draft itself is discarded either way; saving stays a deliberate act. */
  protected applyDraft(): void {
    const d = this.ocrDraft();
    if (!d) return;
    this.reviewError.set(null);
    if (d.lines.length > MAX_LINES) {
      const m = `This receipt lists ${d.lines.length} line items — more than the ${MAX_LINES} a purchase can hold. Enter the lines yourself.`;
      // the refusal is stated inside the review modal the owner is looking at,
      // and the draft stays available: nothing was applied, nothing was lost
      this.reviewError.set(m);
      return;
    }
    const draftCur = (d.currency ?? "").trim().toUpperCase();
    const ownerCur = (this.currency.value || "").trim().toUpperCase();
    if (
      draftCur !== "" &&
      this.currency.dirty &&
      ownerCur !== "" &&
      ownerCur !== draftCur
    ) {
      const m = `This receipt reads as ${draftCur}, but you have ${ownerCur} in the form. Amounts are never converted — change the currency yourself, then apply again.`;
      this.reviewError.set(m);
      return;
    }
    const cur = draftCur || ownerCur || "USD";

    // Only the untouched initial line may be replaced by extracted lines. A
    // plant chosen for the initial allocation, or an extra allocation added by
    // the owner, counts as owner work even before its amounts are typed.
    const initial = this.lines.at(0);
    const initialAllocs = initial?.controls.allocs.controls ?? [];
    const blankInitial =
      this.lines.length === 1 &&
      (initial!.controls.description.value ?? "").trim() === "" &&
      (initial!.controls.total.value ?? "").trim() === "" &&
      !initial!.controls.id.value &&
      initialAllocs.length === 1 &&
      initialAllocs.every(
        (al) =>
          !al.controls.id.value &&
          (al.controls.plantId.value ?? "") === "" &&
          (al.controls.line.value ?? "").trim() === "" &&
          (al.controls.tax.value ?? "").trim() === "" &&
          (al.controls.ship.value ?? "").trim() === "" &&
          (initial!.controls.quantity.value ?? "").trim() === "1",
      );

    // Header writes respect the owner: a control they have typed into keeps
    // their value, so record whether the header overhead actually changes —
    // allocation splits only follow a real header update.
    let overheadUpdated = false;

    // Every write below is a hydration write: cascades stay suppressed until
    // the whole draft is in, so nothing rewrites a value the draft set.
    this.hydrating = true;
    try {
      if (d.seller && !this.headSeller.dirty)
        this.headSeller.setValue(d.seller, { emitEvent: false });
      if (d.purchasedOn && !this.headDate.dirty)
        this.headDate.setValue(d.purchasedOn, { emitEvent: false });
      if (d.currency && !this.currency.dirty)
        this.currency.setValue(d.currency, { emitEvent: false });
      if (d.totalMinor != null && !this.total.dirty)
        this.total.setValue(toInput(d.totalMinor, cur), { emitEvent: false });
      if (d.taxMinor != null && !this.tax.dirty) {
        const v = toInput(d.taxMinor, cur);
        if ((this.tax.value ?? "") !== v) overheadUpdated = true;
        this.tax.setValue(v, { emitEvent: false });
      }
      if (d.shippingMinor != null && !this.shipping.dirty) {
        const v = toInput(d.shippingMinor, cur);
        if ((this.shipping.value ?? "") !== v) overheadUpdated = true;
        this.shipping.setValue(v, { emitEvent: false });
      }
      if (d.lines.length > 0 && blankInitial) {
        this.lines.clear({ emitEvent: false });
        for (const ln of d.lines) {
          const g = this.fb.group({
            id: this.fb.control(""),
            description: this.fb.control(ln.description ?? ""),
            quantity: this.fb.control("1"),
            total: this.fb.control(
              ln.amountMinor != null ? toInput(ln.amountMinor, cur) : "",
            ),
            allocs: this.fb.array<AllocG>([
              this.fb.group({
                id: this.fb.control(""),
                plantId: this.fb.control(""),
                line: this.fb.control(
                  ln.amountMinor != null ? toInput(ln.amountMinor, cur) : "",
                ),
                tax: this.fb.control(""),
                ship: this.fb.control(""),
              }),
            ]),
          });
          this.lines.push(g, { emitEvent: false });
          this.wireLine(g);
        }
      }
      // header overhead splits across the allocations when the draft's lines
      // replaced the blank initial line, or when the header overhead really
      // changed — never when the owner's own lines (and their dirty
      // allocation tax/shipping) are being preserved
      if (blankInitial && d.lines.length > 0) this.distributeOverhead();
      else if (overheadUpdated) this.distributeOverhead();
      // one preview pass for the whole hydration. It emits so the cached
      // value the reconciliation preview reads is refreshed; the hydration
      // guard still suppresses every cascade.
      this.form.updateValueAndValidity({ emitEvent: true, onlySelf: false });
    } finally {
      this.hydrating = false;
    }
    this.ocrDraft.set(null);
    this.reviewEntry()?.markPristine();
    this.reviewing.set(false);
    // Programmatic writes do not bubble input/change events, so the shell's
    // auto tracking cannot see them: mark the entry dirty by hand. Saving
    // stays a deliberate act — applying never submits.
    this.form.markAsDirty();
    this.entry()?.markDirty();
    this.toasts.ok(
      "Extracted values applied — check them against the receipt, then save.",
    );
  }

  private async loadPurchase(id: string): Promise<void> {
    const res = await this.gql.query(
      PurchaseDetailDocument,
      { id },
      this.abort.signal,
    );
    if (res.kind !== "ok") {
      this.loadError.set(
        res.kind === "auth"
          ? "Session expired — sign in again."
          : "Could not reach the server.",
      );
      return;
    }
    if (res.errors.length > 0) {
      this.loadError.set(res.errors[0].message);
      return;
    }
    const p = res.data?.purchase;
    if (!p) {
      this.loadError.set("This purchase is not in your workspace.");
      return;
    }
    this.revision.set(p.revision);
    this.voided.set(p.voidedAt !== null);
    this.receipts.set(p.receipts);
    this.loaded.set(p);
    const cur = p.currency;
    this.form.patchValue(
      {
        headSeller: p.seller ?? "",
        headDate: p.purchasedOn ?? "",
        currency: cur,
        total: toInput(p.totalMinor, cur),
        tax: toInput(p.taxMinor, cur),
        shipping: toInput(p.shippingMinor, cur),
      },
      { emitEvent: false },
    );
    this.lines.clear({ emitEvent: false });
    this.hydrating = true;
    for (const l of p.lines.items) {
      const ln = this.fb.group({
        id: this.fb.control(l.id),
        description: this.fb.control(l.description),
        quantity: this.fb.control(l.quantity),
        total: this.fb.control(toInput(l.lineTotalMinor, cur)),
        allocs: this.fb.array<AllocG>(
          l.allocations.map((a) =>
            this.fb.group({
              id: this.fb.control(a.id),
              plantId: this.fb.control(a.plantId ?? ""),
              line: this.fb.control(toInput(a.lineMinor, cur)),
              tax: this.fb.control(toInput(a.taxMinor, cur)),
              ship: this.fb.control(toInput(a.shippingMinor, cur)),
            }),
          ),
        ),
      });
      this.lines.push(ln, { emitEvent: false });
      this.wireLine(ln);
    }
    // The silent patch still needs exactly one preview pass; the cascades
    // stay suppressed through it so loaded (already-balanced) values stand.
    this.form.updateValueAndValidity({ emitEvent: true, onlySelf: false });
    this.hydrating = false;
  }

  // ── linked allocations ──────────────────────────────────────────────────

  /** allocation id -> {acquisition id, plant name} for recorded prices. */
  private readonly linkedMap = computed(() => {
    const m = new Map<string, { acqId: string; plantName: string }>();
    for (const p of this.plants()) {
      if (p.linkedAllocId && p.acqId)
        m.set(p.linkedAllocId, { acqId: p.acqId, plantName: p.name });
    }
    return m;
  });

  /** Plant name when this allocation is a plant's recorded price. */
  protected linkedName(al: AllocG): string | null {
    const id = al.controls.id.value;
    if (!id) return null;
    return this.linkedMap().get(id)?.plantName ?? null;
  }

  /** Linked allocations the draft drops or moves off their original line,
   * measured against the purchase as loaded — a deleted row is invisible
   * to the draft itself. */
  private readonly loaded = signal<PurchaseFieldsFragment | null>(null);

  protected readonly pendingUnlinks = computed(() => {
    this.draft();
    const p = this.loaded();
    if (!p) return [];
    const lm = this.linkedMap();
    const out: { acqId: string; plantName: string }[] = [];
    for (const l of p.lines.items) {
      for (const a of l.allocations) {
        const link = lm.get(a.id);
        if (!link) continue;
        // Retained = the same allocation id still sits on the same line id.
        const retained = this.lines.controls.some(
          (ln) =>
            ln.controls.id.value === l.id &&
            ln.controls.allocs.controls.some(
              (al) => al.controls.id.value === a.id,
            ),
        );
        if (!retained) out.push(link);
      }
    }
    return out;
  });

  protected unlinkNames(): string {
    const names = this.pendingUnlinks().map((u) => u.plantName);
    return names.length === 1 ? names[0] : names.join(" and ");
  }

  // ── reconciliation preview (exact, mirrors the server invariants) ──────

  protected readonly recon = computed<ReconResult>(() => {
    this.draft();
    const cur = (this.currency.value || "USD").trim().toUpperCase();
    const notes: string[] = [];
    const p = (v: string, required: boolean): ParseRes =>
      parseAmount(v, cur, { required });

    const total = p(this.total.value ?? "", true);
    const tax = p(this.tax.value ?? "", false);
    const ship = p(this.shipping.value ?? "", false);
    if (total.error) notes.push(`Total: ${total.error}`);
    if (tax.error) notes.push(`Tax: ${tax.error}`);
    if (ship.error) notes.push(`Shipping: ${ship.error}`);
    if (!/^[A-Z]{3}$/.test(cur))
      notes.push("Currency: enter a three-letter code (e.g. USD).");

    interface LineP {
      name: string;
      total: ParseRes;
      quantity: string;
      id: string | null;
      allocs: {
        plantId: string;
        id: string | null;
        line: ParseRes;
        tax: ParseRes;
        ship: ParseRes;
      }[];
    }
    const parsed: LineP[] = [];
    for (const ln of this.lines.controls) {
      const name = (ln.controls.description.value ?? "").trim();
      const quantity = (ln.controls.quantity.value ?? "").trim();
      const ltotal = p(ln.controls.total.value ?? "", true);
      const label = name || `line ${parsed.length + 1}`;
      if (name === "")
        notes.push(`Line ${parsed.length + 1}: enter a description.`);
      if (!/^\d+(\.\d+)?$/.test(quantity))
        notes.push(
          `Line ${parsed.length + 1}: quantity must be a positive number.`,
        );
      if (ltotal.error) notes.push(`Line "${label}": ${ltotal.error}`);
      const allocs = ln.controls.allocs.controls.map((al) => ({
        plantId: al.controls.plantId.value || "",
        id: al.controls.id.value || null,
        line: p(al.controls.line.value ?? "", true),
        tax: p(al.controls.tax.value ?? "", false),
        ship: p(al.controls.ship.value ?? "", false),
      }));
      for (const a of allocs) {
        if (a.line.error)
          notes.push(
            `Line "${label}": allocation item amount — ${a.line.error}`,
          );
        if (a.tax.error)
          notes.push(`Line "${label}": allocation tax — ${a.tax.error}`);
        if (a.ship.error)
          notes.push(`Line "${label}": allocation shipping — ${a.ship.error}`);
      }
      parsed.push({
        name,
        total: ltotal,
        quantity,
        id: ln.controls.id.value || null,
        allocs,
      });
    }
    if (parsed.length === 0) notes.push("Add at least one line.");

    if (notes.length > 0) return { ok: false, notes, input: null };

    // All amounts parsed — compare exactly (minor-unit integers).
    if (cmpMinor(addMinor(tax.minor, ship.minor), total.minor) > 0) {
      notes.push(
        `Tax ${fmtMinor(tax.minor, cur)} + shipping ${fmtMinor(ship.minor, cur)} exceeds total ${fmtMinor(total.minor, cur)}.`,
      );
    }
    const net = subMinor(total.minor, addMinor(tax.minor, ship.minor));
    let lineSum = "0";
    let taxSum = "0";
    let shipSum = "0";
    for (const l of parsed) {
      lineSum = addMinor(lineSum, l.total.minor);
      let lsum = "0";
      for (const a of l.allocs) {
        lsum = addMinor(lsum, a.line.minor);
        taxSum = addMinor(taxSum, a.tax.minor);
        shipSum = addMinor(shipSum, a.ship.minor);
      }
      if (lsum !== l.total.minor) {
        notes.push(
          `Allocations for "${l.name}" sum to ${fmtMinor(lsum, cur)} — line total is ${fmtMinor(l.total.minor, cur)} (off by ${fmtDiff(subMinor(lsum, l.total.minor), cur)}).`,
        );
      }
    }
    if (lineSum !== net) {
      notes.push(
        `Line totals sum to ${fmtMinor(lineSum, cur)} — total minus tax and shipping is ${fmtMinor(net, cur)} (off by ${fmtDiff(subMinor(lineSum, net), cur)}).`,
      );
    }
    if (taxSum !== tax.minor) {
      notes.push(
        `Allocation tax amounts sum to ${fmtMinor(taxSum, cur)} — receipt tax is ${fmtMinor(tax.minor, cur)} (off by ${fmtDiff(subMinor(taxSum, tax.minor), cur)}).`,
      );
    }
    if (shipSum !== ship.minor) {
      notes.push(
        `Allocation shipping amounts sum to ${fmtMinor(shipSum, cur)} — receipt shipping is ${fmtMinor(ship.minor, cur)} (off by ${fmtDiff(subMinor(shipSum, ship.minor), cur)}).`,
      );
    }
    if (notes.length > 0) return { ok: false, notes, input: null };

    const lines: PurchaseLineInput[] = parsed.map((l) => ({
      ...(l.id ? { id: l.id } : {}),
      description: l.name,
      quantity: l.quantity,
      lineTotalMinor: l.total.minor,
      allocations: l.allocs.map<PurchaseAllocationInput>((a) => ({
        ...(a.id ? { id: a.id } : {}),
        plantId: a.plantId || null,
        lineMinor: a.line.minor,
        taxMinor: a.tax.minor,
        shippingMinor: a.ship.minor,
      })),
    }));
    const seller = blankToNull(this.headSeller.value ?? "");
    const purchasedOn = blankToNull(this.headDate.value ?? "");
    const input: CreatePurchaseInput = {
      currency: cur,
      ...(seller !== null ? { seller } : {}),
      ...(purchasedOn !== null ? { purchasedOn } : {}),
      totalMinor: total.minor,
      taxMinor: tax.minor,
      shippingMinor: ship.minor,
      lines,
    };
    return { ok: true, notes: [], input };
  });

  // ── submit ──────────────────────────────────────────────────────────────

  /** Stable create key: the same payload retries under the same uuid, so an
   * uncertain network result can never create a second purchase. A changed
   * payload is a different intent and gets a fresh key. */
  private createKeyFor(input: CreatePurchaseInput): string {
    const payload = JSON.stringify(input);
    if (!this.createKey || this.createKey.payload !== payload)
      this.createKey = { payload, key: crypto.randomUUID() };
    return this.createKey.key;
  }

  /** Attach the receipts the editor staged (UI09: a new purchase had no id to
   * attach to). Keys are stable per (purchase, asset), so a retry replays the
   * attach instead of duplicating it. Returns the assets confirmed attached;
   * anything else — transport, GraphQL errors, payload problems — leaves the
   * staged entries in place so the next save retries them. */
  private async attachStaged(
    purchaseId: string,
  ): Promise<{ attached: string[]; note: string | null }> {
    const attached: string[] = [];
    for (const s of this.stagedReceipts()) {
      try {
        const res = await this.gql.mutate(AttachReceiptDocument, {
          input: { purchaseId, assetId: s.assetId },
          clientMutationId: `purchase-receipt-${purchaseId}-${s.assetId}`,
        });
        if (res.kind !== "ok") return { attached, note: RECEIPT_ATTACH_NOTE };
        if (res.errors.length > 0)
          return { attached, note: RECEIPT_ATTACH_NOTE };
        const payload = res.data?.attachReceipt;
        const problems = (payload?.problems ?? []) as MediaProblem[];
        if (!payload?.receipt || problems.length > 0)
          return { attached, note: RECEIPT_ATTACH_NOTE };
        attached.push(s.assetId);
      } catch {
        return { attached, note: RECEIPT_ATTACH_NOTE };
      }
    }
    return { attached, note: null };
  }

  /** Fold a saved purchase back into the editor without touching what the
   * owner typed: the header values on screen are the ones that were just
   * accepted. Generated line/allocation ids are re-synchronised by position,
   * which the server preserves (frozen order), so a later save updates the
   * rows it created instead of inserting new ones. */
  private adoptSaved(p: PurchaseFieldsFragment): void {
    this.editId.set(p.id);
    this.revision.set(p.revision);
    this.voided.set(p.voidedAt !== null);
    this.loaded.set(p);
    this.receipts.set(p.receipts);
    // The id writes below are silent, and the reconciliation preview reads a
    // value snapshot cached from valueChanges — so without one emitting pass
    // a retry would rebuild its payload from the pre-save value and send no
    // saved line/allocation ids at all. The hydration guard makes that pass
    // safe: it refreshes the cache without letting any cascade rewrite a
    // value the owner just had accepted.
    const wasHydrating = this.hydrating;
    this.hydrating = true;
    try {
      for (let i = 0; i < this.lines.length; i++) {
        const saved = p.lines.items[i];
        if (!saved) continue;
        const ln = this.lines.at(i)!;
        ln.controls.id.setValue(saved.id, { emitEvent: false });
        for (let j = 0; j < ln.controls.allocs.length; j++) {
          const savedAlloc = saved.allocations[j];
          if (!savedAlloc) continue;
          ln.controls.allocs
            .at(j)!
            .controls.id.setValue(savedAlloc.id, { emitEvent: false });
        }
      }
      this.form.updateValueAndValidity({ emitEvent: true, onlySelf: false });
    } finally {
      this.hydrating = wasHydrating;
    }
  }

  protected async submit(): Promise<void> {
    if (this.submitting() || this.receiptBusy() || this.voided()) return;
    this.attempted.set(true);
    this.form.markAllAsTouched();
    const r = this.recon();
    const unlinks = this.pendingUnlinks();
    if (unlinks.length > 0 && !this.unlinkConfirmed()) {
      this.unlinkWarn.set(true);
      return;
    }
    if (!r.ok || !r.input) return;
    this.unlinkWarn.set(false);
    this.conflict.set(null);
    this.general.set([]);
    this.submitting.set(true);
    try {
      const id = this.editId();
      let res;
      if (id) {
        res = await this.gql.mutate(UpdatePurchaseDocument, {
          input: {
            id,
            expectedRevision: this.revision() ?? 1,
            ...r.input,
            ...(unlinks.length > 0
              ? { unlinkAcquisitionIds: unlinks.map((u) => u.acqId) }
              : {}),
          },
          clientMutationId: `${crypto.randomUUID()}:upd`,
        });
      } else {
        res = await this.gql.mutate(CreatePurchaseDocument, {
          input: r.input,
          clientMutationId: `${this.createKeyFor(r.input)}:create`,
        });
      }
      if (res.kind === "auth") {
        this.toasts.error("Session expired — sign in again.");
        return;
      }
      if (res.kind !== "ok") {
        this.toasts.error("Could not reach the server. Nothing was saved.");
        return;
      }
      if (res.errors.length > 0) {
        // A GraphQL-level error is not a saved purchase, whatever the
        // transport looked like.
        this.general.update((cur) => [
          ...cur,
          ...res.errors.map((e) => e.message),
        ]);
        return;
      }
      const payload = id
        ? (res.data as UpdatePurchaseMutation | null)?.updatePurchase
        : (res.data as CreatePurchaseMutation | null)?.createPurchase;
      const problems = (payload?.problems ?? []) as PayloadProblem[];
      if (problems.length > 0) {
        const cnote = conflictNote(problems, "This purchase");
        if (cnote) {
          // Draft preserved: the form is never patched on a conflict.
          this.conflict.set(cnote);
          return;
        }
        const msgs = [
          ...Object.values(problemMap(problems)),
          ...generalProblems(problems),
        ];
        const shown = msgs.length > 0 ? msgs : [problems[0].message];
        this.general.update((cur) => [
          ...cur,
          ...shown.filter((m) => !cur.includes(m)),
        ]);
        return;
      }
      // Real success needs the purchase itself — a payload with neither a
      // purchase nor a problem is not a save.
      const saved = payload?.purchase;
      if (!saved) {
        this.general.update((cur) => [
          ...cur,
          "The server did not return the saved purchase, so it may not have been recorded. Check the ledger before saving again.",
        ]);
        return;
      }
      this.adoptSaved(saved);
      this.createKey = null;
      let attachNote: string | null = null;
      if (this.stagedReceipts().length > 0) {
        const att = await this.attachStaged(saved.id);
        if (att.attached.length > 0)
          this.stagedReceipts.update((cur) =>
            cur.filter((s) => !att.attached.includes(s.assetId)),
          );
        attachNote = att.note;
      }
      if (attachNote) {
        // The purchase is saved; the receipt is not attached. Stay in the
        // editor with the staged image still pending — no success toast, no
        // navigation, and the form stays dirty so the retry path is real.
        this.general.update((cur) => [
          ...cur,
          ...(cur.includes(attachNote) ? [] : [attachNote]),
        ]);
        return;
      }
      this.toasts.ok(
        id
          ? "Purchase updated."
          : "Purchase added — one expense, split exactly as allocated.",
      );
      // Save approval first: the entry guard must not prompt on the success
      // navigation, and the form is now the saved state.
      this.form.markAsPristine();
      this.entry()?.markPristine();
      await this.refreshReceipts();
      await this.router.navigate(["/purchases"]);
    } catch {
      // An unexpected exception says nothing about the server's side, so the
      // message stays honest about what is and is not known.
      this.general.update((cur) => [
        ...cur,
        this.editId()
          ? "The save could not be completed — the purchase may already be saved. Check the ledger, then try again."
          : "The save could not be completed. Nothing was created.",
      ]);
    } finally {
      this.submitting.set(false);
    }
  }
}
