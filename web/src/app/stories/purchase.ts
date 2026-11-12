import { Component, computed, signal } from '@angular/core';
import { UiField } from '../ui/field';
import { UiStatus } from '../ui/status';
import { UiToasts } from '../ui/toast';
import { inject } from '@angular/core';
import {
  StoryBar,
  StoryCredits,
  StoryError,
  StorySession,
} from './shared';
import { StoryState, purchase } from '../../fixtures/collection';

@Component({
  selector: 'story-purchase',
  imports: [StoryBar, StoryError, StorySession, StoryCredits, UiField, UiStatus],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    @switch (state()) {
      @case ('error') { <story-error title="Purchase failed to load" message="The receipt is safe on the server. Retry to edit." /> }
      @case ('offline') { <story-error title="You are offline" message="Purchases cannot be edited without a connection." /> }
      @case ('session') { <story-session /> }
      @case ('empty') { <story-error title="No line items" message="Add at least one item, or delete the purchase." /> }
      @case ('conflict') { <story-error title="Already edited elsewhere" message="This purchase changed while you were editing. Your draft is kept; reload to merge." /> }
      @default {
        <h1>Purchase — {{ purchase.seller }}</h1>
        <p class="small" style="color:var(--text-muted)">
          OCR draft values are highlighted until you confirm them. Every value is correctable.
        </p>

        <div class="grid-cards" style="grid-template-columns: 2fr 1fr; align-items:start; margin-top:var(--sp-6)">
          <section class="stack">
            <div class="card">
              <div class="row" style="gap:var(--sp-4)">
                <div style="flex:2"><ui-field label="Seller" inputId="p-seller" /></div>
                <div style="flex:1"><ui-field label="Date" type="date" inputId="p-date" /></div>
              </div>
            </div>

            <div class="card">
              <h2>Line items</h2>
              <table style="width:100%; border-collapse:collapse" aria-label="Line items">
                <thead>
                  <tr>
                    <th class="caption" style="text-align:start; padding:var(--sp-2)">Item</th>
                    <th class="caption" style="text-align:end; padding:var(--sp-2)">Amount</th>
                    <th class="caption" style="text-align:start; padding:var(--sp-2)">Allocated to</th>
                  </tr>
                </thead>
                <tbody>
                  @for (l of purchase.lines; track l.desc) {
                    <tr style="border-top:1px solid var(--border-decorative)">
                      <td style="padding:var(--sp-2)">
                        <span class="small">{{ l.desc }}</span>
                        @if (l.ocrDraft) { <span class="badge" style="margin-left:var(--sp-2)">OCR draft — unconfirmed</span> }
                      </td>
                      <td class="small num" style="text-align:end; padding:var(--sp-2)">{{ l.amount.toFixed(2) }}</td>
                      <td style="padding:var(--sp-2)">
                        @if (l.allocated.length) {
                          <span class="small">{{ l.allocated.join(', ') }}</span>
                        } @else {
                          <ui-status kind="warn" label="Unallocated" />
                        }
                      </td>
                    </tr>
                  }
                </tbody>
              </table>
            </div>

            <div class="card row spread">
              <div>
                <p class="small" style="margin:0">Items $65.75 + overhead {{ purchase.overhead.toFixed(2) }}</p>
                <p class="small" style="margin:var(--sp-1) 0 0"><strong>Reconciled total</strong> <span class="num">$70.74</span></p>
                <p class="caption">{{ purchase.unknownCostCount }} line with unknown cost excluded from per-plant totals</p>
              </div>
              <div class="row">
                @if (saveBlocked()) {
                  <span class="field-error" role="text">Confirm the two OCR draft lines before saving.</span>
                }
                <button type="button" class="btn btn-primary" [disabled]="saveBlocked()" (click)="toasts.ok('Purchase saved')">Save</button>
              </div>
            </div>
          </section>

          <aside class="card hero-panel" style="padding:0; overflow:hidden">
            <img [src]="purchase.receipt" alt="Original receipt scan for this purchase"
                 style="width:100%; aspect-ratio:3/4; object-fit:cover" />
            <div style="padding:var(--sp-3) var(--sp-4)">
              <div class="row spread">
                <strong class="small">receipt-2026-09-14.jpg</strong>
                <a class="small" href="/assets/photos/monstera2.jpg" download>Download</a>
              </div>
              <p class="caption">Original receipt, kept unmodified.</p>
            </div>
          </aside>
        </div>
      }
    }
  `,
})
export class StoryPurchase {
  protected readonly state = signal<StoryState>('ready');
  protected readonly purchase = purchase;
  protected readonly saveBlocked = signal(true);
  protected readonly toasts = inject(UiToasts);
}
