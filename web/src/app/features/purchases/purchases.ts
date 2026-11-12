import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  inject,
  signal,
  viewChild,
} from "@angular/core";
import {
  ActivatedRoute,
  Router,
  RouterLink,
  RouterOutlet,
} from "@angular/router";
import {
  PurchasesDocument,
  SpendingDocument,
  VoidPurchaseDocument,
  type PurchaseFieldsFragment,
  type PurchaseFilter,
} from "@canopy/contracts";
import { GraphQLClient, type GqlResult } from "../../core/graphql/client";
import { PageStore, type Page } from "../../core/graphql/page-store";
import { UiEntryDialog } from "../../ui/entry-dialog";
import { UiSkeleton } from "../../ui/skeleton";
import { UiToasts } from "../../ui/toast";
import { fmtMinor } from "./money";
import { conflictNote, type PayloadProblem } from "../collection/shared";

interface SpendingView {
  byCurrency: { currency: string; total: string }[];
  unknownCost: number;
  missingRecord: number;
}

/**
 * Purchases screen (T12): the workspace ledger. One row per receipt —
 * a shared $100.00 purchase is one expense, never two — plus the
 * spending summary: per-currency totals (never merged), acquisitions
 * with unknown cost and plants with no acquisition record, each stated
 * honestly. The table reflows to stacked cards on mobile.
 *
 * UI02: the two-step void confirmation is the shared compact UiEntryDialog
 * instead of an inline swap inside the row. Same mutation, same expected
 * revision, same honest problem reporting; the confirmation stays mounted
 * while the mutation is in flight and closes only on success, so a failure
 * or conflict keeps its message visible with the retry in place. Voided
 * records stay read-only — nothing here deletes.
 */
@Component({
  selector: "purchases-page",
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, RouterOutlet, UiEntryDialog, UiSkeleton],
  templateUrl: "./purchases.html",
  styleUrl: "./purchases.css",
})
export class PurchasesPage implements OnDestroy {
  private readonly gql = inject(GraphQLClient);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly toasts = inject(UiToasts);
  private readonly abort = new AbortController();

  protected readonly q = signal("");
  protected readonly cur = signal("");
  protected readonly showVoided = signal(false);
  protected readonly hasNext = signal(false);
  protected readonly spending = signal<SpendingView | null>(null);
  /** the purchase whose void confirmation is on screen (declarative [open]) */
  protected readonly voiding = signal<PurchaseFieldsFragment | null>(null);
  /** the void mutation is in flight: blocks dismissal, prevents a double submit */
  protected readonly voidBusy = signal(false);
  /** honest failure/conflict text shown inside the confirmation itself */
  protected readonly voidError = signal("");

  protected readonly store: PageStore<PurchaseFieldsFragment>;

  /** The shared confirmation handle, so Cancel goes through requestClose(). */
  private readonly voidEntry = viewChild<UiEntryDialog>("voidEntry");

  constructor() {
    this.store = new PageStore((after) => this.fetchPage(after));
    this.route.queryParamMap.subscribe((pm) => {
      this.q.set(pm.get("q") ?? "");
      this.cur.set(pm.get("cur") ?? "");
      this.showVoided.set(pm.get("voided") === "1");
      void this.store.refresh();
    });
    void this.loadSpending();
  }

  ngOnDestroy(): void {
    this.abort.abort();
    this.store.destroy();
  }

  protected fmt(minor: string, currency: string): string {
    return fmtMinor(minor, currency);
  }

  /** A receipt link appears only when an actual attached asset exists. */
  protected receiptUrl(p: PurchaseFieldsFragment): string | null {
    const first = p.receipts[0];
    return first ? `/api/media/${first.asset.id}/original` : null;
  }

  protected setParams(patch: Record<string, string | null>): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: patch,
      queryParamsHandling: "merge",
    });
  }

  protected search(value: string): void {
    this.setParams({ q: value.trim() || null });
  }

  protected clearFilters(): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { q: null, cur: null },
    });
  }

  protected retry(): void {
    void this.store.refresh();
    void this.loadSpending();
  }

  /** The purchase form routes (new / :id/edit) render in this page's outlet.
   * When they deactivate — saved or dismissed — the list and the spending
   * summary both re-read, so a new or edited purchase shows immediately in
   * each without remounting this parent page. */
  protected onOutletDeactivate(): void {
    void this.store.refresh();
    void this.loadSpending();
  }

  private filter(): PurchaseFilter {
    return {
      ...(this.q() ? { sellerQuery: this.q() } : {}),
      ...(this.cur() ? { currency: this.cur() } : {}),
      ...(this.showVoided() ? { includeVoided: true } : {}),
    };
  }

  private fetchPage(
    after: string | null,
  ): Promise<GqlResult<Page<PurchaseFieldsFragment>>> {
    // first:20 matches the collection page size; the server's complexity
    // budget is sized for the frozen Purchases operation at its default.
    return this.gql
      .query(
        PurchasesDocument,
        { first: 20, after, filter: this.filter() },
        this.abort.signal,
      )
      .then((res) => {
        if (res.kind !== "ok") return res;
        if (!res.data) return { kind: "ok", data: null, errors: res.errors };
        const conn = res.data.purchases;
        this.hasNext.set(conn.pageInfo.hasNextPage);
        return {
          kind: "ok",
          errors: res.errors,
          data: {
            items: conn.items,
            endCursor: conn.pageInfo.endCursor,
            hasNextPage: conn.pageInfo.hasNextPage,
          },
        };
      });
  }

  private async loadSpending(): Promise<void> {
    const res = await this.gql.query(SpendingDocument, {}, this.abort.signal);
    if (res.kind === "ok" && res.data) {
      this.spending.set({
        byCurrency: res.data.spending.knownSpendingByCurrency.map((c) => ({
          currency: c.currency,
          total: c.total.minorUnits,
        })),
        unknownCost: res.data.spending.unknownAcquisitionCostCount,
        missingRecord: res.data.spending.missingAcquisitionRecordCount,
      });
    }
  }

  /** Opens the shared confirmation. Nothing is sent here — the void waits for
   * the explicit Confirm void action inside the dialog. */
  protected openVoid(p: PurchaseFieldsFragment): void {
    if (this.voidBusy()) return;
    this.voidError.set("");
    this.voiding.set(p);
  }

  /** Cancel / X / Escape / backdrop. Guarded while the mutation runs. */
  protected closeVoid(): void {
    this.voidEntry()?.requestClose();
  }

  protected onVoidDismissed(): void {
    this.voiding.set(null);
    this.voidError.set("");
  }

  /** Same two-step mutation and revision check as before. The confirmation
   * stays mounted through the request: it closes only on an accepted void,
   * and any failure or conflict stays visible with the retry in place. */
  protected async confirmVoid(): Promise<void> {
    const p = this.voiding();
    if (!p || this.voidBusy()) return;
    this.voidBusy.set(true);
    this.voidError.set("");
    const res = await this.gql.mutate(VoidPurchaseDocument, {
      input: { id: p.id, expectedRevision: p.revision },
      clientMutationId: `${crypto.randomUUID()}:void`,
    });
    this.voidBusy.set(false);
    if (res.kind === "auth") {
      this.voidError.set("Session expired — sign in again. Nothing changed.");
      return;
    }
    if (res.kind !== "ok") {
      this.voidError.set("Could not reach the server. Nothing changed.");
      return;
    }
    const problems = (res.data?.voidPurchase?.problems ??
      []) as PayloadProblem[];
    if (problems.length > 0) {
      const note = conflictNote(problems, "This purchase");
      this.voidError.set(note || problems[0].message);
      void this.store.refresh();
      return;
    }
    // accepted: approve the entry, then close the confirmation
    this.voidEntry()?.markPristine();
    this.voiding.set(null);
    this.voidError.set("");
    this.toasts.ok(
      "Purchase voided — any plant priced from it now shows cost unknown.",
    );
    void this.store.refresh();
    void this.loadSpending();
  }
}
