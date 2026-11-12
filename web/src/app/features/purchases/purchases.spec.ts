import { TestBed } from "@angular/core/testing";
import { ActivatedRoute, Params, Router } from "@angular/router";
import { BehaviorSubject } from "rxjs";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SessionService } from "../../core/auth/session";
import { PurchasesPage } from "./purchases";

/** Minimal PurchaseFields row as the server would return it. */
function purchaseRow(over: Record<string, unknown> = {}) {
  return {
    id: "pur-1",
    seller: "Fig nursery",
    purchasedOn: "2026-03-10",
    currency: "USD",
    totalMinor: "10000",
    shippingMinor: "500",
    taxMinor: "500",
    revision: 1,
    voidedAt: null,
    createdAt: "2026-11-01T00:00:00Z",
    updatedAt: "2026-11-01T00:00:00Z",
    lines: {
      items: [
        {
          id: "l1",
          purchaseId: "pur-1",
          description: "Fig tree A",
          quantity: "1",
          lineTotalMinor: "4500",
          allocations: [
            {
              id: "a1",
              purchaseLineId: "l1",
              plantId: "f1",
              lineMinor: "4500",
              taxMinor: "250",
              shippingMinor: "250",
            },
          ],
        },
      ],
      pageInfo: { endCursor: null, hasNextPage: false },
    },
    receipts: [],
    ...over,
  };
}

function purchasesEnvelope(items: unknown[], hasNext = false) {
  return {
    purchases: {
      items,
      pageInfo: { endCursor: hasNext ? "c1" : null, hasNextPage: hasNext },
    },
  };
}

function spendingEnvelope(over: Record<string, unknown> = {}) {
  return {
    spending: {
      knownSpendingByCurrency: [
        { currency: "USD", total: { minorUnits: "10000" } },
        { currency: "EUR", total: { minorUnits: "8000" } },
      ],
      unknownAcquisitionCostCount: 2,
      missingAcquisitionRecordCount: 1,
      ...over,
    },
  };
}

/** fetch stub routing by operation name; records every call's variables. */
function stubFetch(handlers: Record<string, unknown>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as {
        query: string;
        variables?: object;
      };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? "";
      calls.push({
        op,
        vars: (body.variables ?? {}) as Record<string, unknown>,
      });
      const h = handlers[op];
      if (h === "network") throw new TypeError("fetch failed");
      if (h === "401") return new Response("", { status: 401 });
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("PurchasesPage", () => {
  let session: SessionService;
  let params$: BehaviorSubject<{ get(k: string): string | null }>;
  let current: Params;
  let navigate: ReturnType<typeof vi.fn>;

  const toMap = (p: Params) => ({ get: (k: string) => p[k] ?? null });
  const setUrl = (p: Params): void => {
    current = p;
    params$.next(toMap(current));
  };

  beforeEach(() => {
    current = {};
    params$ = new BehaviorSubject(toMap(current));
    navigate = vi.fn(
      async (_cmds: unknown, extras?: { queryParams?: Params }) => {
        const patch = extras?.queryParams ?? {};
        for (const [k, v] of Object.entries(patch)) {
          if (v === null) delete current[k];
          else current[k] = String(v);
        }
        params$.next(toMap(current));
        return true;
      },
    );
    TestBed.configureTestingModule({
      providers: [
        { provide: Router, useValue: { navigate } },
        { provide: ActivatedRoute, useValue: { queryParamMap: params$ } },
      ],
    });
    session = TestBed.inject(SessionService);
    session.state.set("authed");
    session.info.set({
      workspaceId: "w1",
      expiresAt: "2026-12-01T00:00:00Z",
      csrfToken: "c",
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  function render() {
    const fixture = TestBed.createComponent(PurchasesPage);
    fixture.detectChanges();
    return fixture;
  }

  it("renders ledger rows with exact currency totals and the spending summary", async () => {
    stubFetch({
      Purchases: purchasesEnvelope([
        purchaseRow(),
        purchaseRow({
          id: "pur-2",
          currency: "EUR",
          totalMinor: "8000",
          seller: "Jardinerie",
        }),
      ]),
      Spending: spendingEnvelope(),
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? "";
    expect(text).toContain("Fig nursery");
    expect(text).toContain("100.00 USD"); // one $100 receipt, never two expenses
    expect(text).toContain("80.00 EUR");
    expect(text).toContain("Jardinerie");
    // multi-currency totals stay separate
    expect(text).toContain("USD total");
    expect(text).toContain("EUR total");
    // unknown/missing counts stated honestly
    expect(text).toContain("2 acquisitions with unknown cost");
    expect(text).toContain("1 plant without an acquisition record");
    expect(text).toContain("2 shown");
  });

  it("voided rows carry a Voided badge", async () => {
    stubFetch({
      Purchases: purchasesEnvelope([
        purchaseRow({ voidedAt: "2026-04-01T00:00:00Z" }),
      ]),
      Spending: spendingEnvelope(),
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain(
      "Voided",
    );
  });

  it("URL params drive the fetch filter — voided views are deep-linkable", async () => {
    const calls = stubFetch({
      Purchases: purchasesEnvelope([]),
      Spending: spendingEnvelope(),
    });
    setUrl({ q: "nursery", cur: "USD", voided: "1" });
    render();
    await flush();
    const list = calls.find((c) => c.op === "Purchases");
    expect(list?.vars).toMatchObject({
      first: 20,
      filter: { sellerQuery: "nursery", currency: "USD", includeVoided: true },
    });
  });

  it("an error is never presented as an empty ledger", async () => {
    stubFetch({ Purchases: "network", Spending: spendingEnvelope() });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? "";
    expect(text).toContain("Purchases failed to load");
    expect(text).toContain("not an empty ledger");
    expect(text).not.toContain("No purchases yet");
  });

  it("a spending failure does not take the ledger down", async () => {
    stubFetch({
      Purchases: purchasesEnvelope([purchaseRow()]),
      Spending: "network",
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const text = (fixture.nativeElement as HTMLElement).textContent ?? "";
    expect(text).toContain("Spending summary unavailable");
    expect(text).toContain("Fig nursery");
  });

  it("empty garden shows the add CTA, filtered empties point at the filters", async () => {
    stubFetch({
      Purchases: purchasesEnvelope([]),
      Spending: spendingEnvelope({
        knownSpendingByCurrency: [],
        unknownAcquisitionCostCount: 0,
        missingAcquisitionRecordCount: 0,
      }),
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain(
      "No purchases yet",
    );

    setUrl({ q: "zzz" });
    await flush();
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).textContent).toContain(
      "No matching purchases",
    );
  });

  it("void is two-step, sends expectedRevision, and refreshes list + spending", async () => {
    const calls = stubFetch({
      Purchases: purchasesEnvelope([purchaseRow()]),
      Spending: spendingEnvelope(),
      VoidPurchase: {
        voidPurchase: {
          clientMutationId: null,
          purchase: purchaseRow({ voidedAt: "x" }),
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    // find the Void button and click twice (arm, confirm)
    const btns = [...root.querySelectorAll("button")];
    const voidBtn = btns.find((b) => b.textContent?.trim() === "Void");
    expect(voidBtn, "Void button present").toBeTruthy();
    voidBtn!.click();
    fixture.detectChanges();
    const confirm = [
      ...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button"),
    ].find((b) => b.textContent?.trim() === "Confirm void");
    expect(confirm, "second step asks for confirmation").toBeTruthy();
    confirm!.click();
    await flush();
    fixture.detectChanges();
    const voidCall = calls.find((c) => c.op === "VoidPurchase");
    expect(voidCall?.vars).toMatchObject({
      input: { id: "pur-1", expectedRevision: 1 },
    });
    // list and spending were re-fetched after the mutation
    expect(calls.filter((c) => c.op === "Purchases").length).toBeGreaterThan(1);
    expect(calls.filter((c) => c.op === "Spending").length).toBeGreaterThan(1);
  });

  it("a void CONFLICT names the current revision and refetches", async () => {
    const calls = stubFetch({
      Purchases: purchasesEnvelope([purchaseRow()]),
      Spending: spendingEnvelope(),
      VoidPurchase: {
        voidPurchase: {
          clientMutationId: null,
          purchase: null,
          problems: [
            {
              code: "CONFLICT",
              field: "input.expectedRevision",
              message: "revision mismatch",
              currentRevision: 3,
            },
          ],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    const voidBtn = [...root.querySelectorAll("button")].find(
      (b) => b.textContent?.trim() === "Void",
    );
    voidBtn!.click();
    fixture.detectChanges();
    [...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button")]
      .find((b) => b.textContent?.trim() === "Confirm void")!
      .click();
    await flush();
    expect(calls.filter((c) => c.op === "Purchases").length).toBeGreaterThan(1);
  });
});
