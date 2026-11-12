import { Component, signal } from '@angular/core';
import { UiSkeleton } from '../ui/skeleton';
import { StoryBar, StoryCredits, StoryError, StorySession } from './shared';
import { StoryState, purchases, spendingSummary } from '../../fixtures/collection';

/**
 * Purchases ledger visual reference (T12): spending card, filters and the
 * exact-total table the real page renders, including the ≤767px card reflow
 * and an honest voided row. Multi-currency totals never merge.
 */
@Component({
  selector: 'story-purchases',
  imports: [StoryBar, StoryError, StorySession, StoryCredits, UiSkeleton],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    <div class="row spread" style="margin-top:var(--sp-4)">
      <h1 style="margin:0">Purchases</h1>
      <button type="button" class="btn btn-primary">New purchase</button>
    </div>

    <section class="card" aria-labelledby="sp-h" style="margin-top:var(--sp-4); max-width:620px">
      <h2 id="sp-h" style="margin-top:0">Spending</h2>
      <dl class="spend-grid">
        @for (c of spending.byCurrency; track c.currency) {
          <div>
            <dt class="caption">{{ c.currency }} total</dt>
            <dd class="num" style="margin:0">{{ c.total }}</dd>
          </div>
        }
      </dl>
      <p class="caption" style="margin:var(--sp-2) 0 0">
        {{ spending.unknownCostCount }} acquisitions with unknown cost and
        {{ spending.missingRecordCount }} plant without an acquisition record are excluded from
        these totals.
      </p>
    </section>

    <div class="row wrap" role="search" style="margin-top:var(--sp-4)">
      <span class="field-label">Seller</span>
      <input class="input" placeholder="seller contains" style="max-width:240px" />
      <span class="field-label">Currency</span>
      <input class="input" placeholder="e.g. USD" maxlength="3" style="max-width:90px" />
      <button type="button" class="btn">Search</button>
      <button type="button" class="chip" aria-pressed="true">Show voided</button>
    </div>

    @switch (state()) {
      @case ('loading') {
        <div class="stack" aria-busy="true">
          @for (i of [1, 2, 3]; track i) {
            <ui-skeleton ar="16 / 3" />
          }
        </div>
      }
      @case ('empty') {
        <div class="card" style="max-width:560px">
          <h2>No purchases yet</h2>
          <p class="caption">
            Record a receipt once and split it across the plants it bought — per-plant totals come
            from the allocations, the workspace total stays one expense.
          </p>
          <div><button type="button" class="btn btn-primary">Add your first purchase</button></div>
        </div>
      }
      @case ('error') {
        <story-error
          title="Purchases failed to load"
          message="This is not an empty ledger — nothing was changed."
        />
      }
      @case ('offline') {
        <story-error
          title="You are offline"
          message="The ledger needs a connection. Nothing was changed locally."
        />
      }
      @case ('conflict') {
        <story-error
          title="Not saved"
          message="This purchase changed on another device (now revision 3). Nothing was saved — reload and try again."
        />
      }
      @case ('session') {
        <story-session />
      }
      @default {
        <table class="p-table" aria-label="Purchases" style="margin-top:var(--sp-4)">
          <thead>
            <tr>
              <th scope="col">Purchased</th>
              <th scope="col">Seller</th>
              <th scope="col" class="num-h">Total</th>
              <th scope="col">Lines</th>
              <th scope="col">Status</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            @for (p of shown(); track p.id) {
              <tr>
                <td data-label="Purchased">{{ p.purchasedOn ?? 'No date' }}</td>
                <td data-label="Seller">{{ p.seller ?? '—' }}</td>
                <td data-label="Total" class="num">{{ p.total }}</td>
                <td data-label="Lines">{{ p.lines }}</td>
                <td data-label="Status">
                  @if (p.voided) {
                    <span class="badge">Voided</span>
                  } @else {
                    <span class="caption">Active</span>
                  }
                </td>
                <td data-label="Actions">
                  <div class="row">
                    <button type="button" class="btn">Edit</button>
                    @if (!p.voided) {
                      <button type="button" class="btn">Void</button>
                    }
                  </div>
                </td>
              </tr>
            }
          </tbody>
        </table>
        <p class="caption" style="margin-top:var(--sp-3)">4 shown</p>
      }
    }
  `,
  // Same scoped styles as the real ledger page so the reference matches it,
  // including the ≤767px card reflow.
  styles: [
    `
      .spend-grid {
        display: flex;
        gap: var(--sp-6);
        margin: 0;
        flex-wrap: wrap;
      }
      .spend-grid dt {
        font-size: 12px;
      }
      .spend-grid dd {
        font-weight: 600;
      }
      .p-table {
        width: 100%;
        border-collapse: collapse;
      }
      .p-table th,
      .p-table td {
        padding: var(--sp-2) var(--sp-3);
        text-align: start;
        font-size: 14px;
      }
      .p-table th {
        color: var(--text-muted);
        font-weight: 500;
        border-bottom: 1px solid var(--border);
      }
      .p-table tr {
        border-bottom: 1px solid var(--border-decorative);
      }
      .p-table .num-h,
      .p-table td[data-label='Total'] {
        text-align: end;
      }
      @media (max-width: 767px) {
        .p-table thead {
          display: none;
        }
        .p-table,
        .p-table tbody,
        .p-table tr,
        .p-table td {
          display: block;
        }
        .p-table tr {
          border: 1px solid var(--border);
          border-radius: var(--radius-card);
          background: var(--surface-raised);
          padding: var(--sp-2) var(--sp-3);
          margin-bottom: var(--sp-3);
        }
        .p-table td {
          display: flex;
          justify-content: space-between;
          gap: var(--sp-3);
          border: 0;
          text-align: start;
          padding: var(--sp-1) 0;
        }
        .p-table td::before {
          content: attr(data-label);
          color: var(--text-muted);
        }
      }
    `,
  ],
})
export class StoryPurchases {
  protected readonly state = signal<StoryState>('ready');
  protected readonly spending = spendingSummary;
  protected readonly shown = signal(purchases);
}
