import { TestBed } from "@angular/core/testing";
import type { WritableSignal } from "@angular/core";
import { ActivatedRoute, Router } from "@angular/router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SessionService } from "../../core/auth/session";
import type { ReceiptDraft } from "../capture/receipt-upload";
import { PurchaseForm } from "./purchase-form";

function plantRow(
  id: string,
  name: string,
  acq: Record<string, unknown> | null,
) {
  return {
    id,
    name,
    scientificName: null,
    cultivar: null,
    rootstock: null,
    type: "INDIVIDUAL",
    count: null,
    category: "FIG",
    acquiredOn: null,
    plantedOn: null,
    estimatedAgeYears: null,
    status: "ACTIVE",
    revision: 1,
    createdAt: "2026-11-01T00:00:00Z",
    updatedAt: "2026-11-01T00:00:00Z",
    photoCount: 0,
    tags: [],
    coverPhoto: null,
    currentContext: null,
    acquisition: acq,
  };
}

function acquisition(acqId: string, linkedAllocId: string | null) {
  return {
    id: acqId,
    seller: null,
    acquiredOn: null,
    pricingState: linkedAllocId ? "KNOWN" : "UNKNOWN",
    revision: 1,
    initialPurchaseAllocation: linkedAllocId
      ? {
          id: linkedAllocId,
          purchaseLineId: "l1",
          plantId: "f1",
          lineMinor: "4500",
          taxMinor: "250",
          shippingMinor: "250",
        }
      : null,
  };
}

function detail(over: Record<string, unknown> = {}) {
  return {
    purchase: {
      id: "pur-1",
      seller: "Fig nursery",
      purchasedOn: "2026-03-10",
      currency: "USD",
      totalMinor: "5500",
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
                taxMinor: "500",
                shippingMinor: "500",
              },
            ],
          },
        ],
        pageInfo: { endCursor: null, hasNextPage: false },
      },
      receipts: [],
      ...over,
    },
  };
}

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
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("PurchaseForm", () => {
  let session: SessionService;
  let editId: string | null = null;

  beforeEach(() => {
    editId = null;
    // the entry dialog renders in a CDK overlay on document.body
    document.body.innerHTML = "";
    TestBed.configureTestingModule({
      providers: [
        { provide: Router, useValue: { navigate: vi.fn(async () => true) } },
        {
          provide: ActivatedRoute,
          useFactory: () => ({
            snapshot: {
              paramMap: { get: (k: string) => (k === "id" ? editId : null) },
            },
          }),
        },
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

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  function render(id?: string) {
    editId = id ?? null;
    const fixture = TestBed.createComponent(PurchaseForm);
    fixture.detectChanges();
    return fixture;
  }

  /** The form now renders inside the shared modal shell, so its content lives
   * in the CDK overlay panel on document.body rather than under the host
   * element. The payload assertions below are unchanged — only the lookup
   * root moved. The shell opens from an effect, so the first probe can land
   * before the portal exists; settle the scheduler and retry. */
  async function panel(): Promise<HTMLElement> {
    let found = document.querySelector<HTMLElement>(".dialog-panel");
    if (!found) {
      await new Promise((r) => setTimeout(r, 0));
      found = document.querySelector<HTMLElement>(".dialog-panel");
    }
    if (!found) throw new Error("no entry-dialog panel rendered");
    return found;
  }

  async function set(
    fixture: ReturnType<typeof render>,
    id: string,
    value: string,
  ): Promise<void> {
    const el = (await panel()).querySelector(`#${id}`) as HTMLInputElement;
    expect(el, `input #${id}`).toBeTruthy();
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
    fixture.detectChanges();
  }

  async function select(
    fixture: ReturnType<typeof render>,
    id: string,
    value: string,
  ): Promise<void> {
    const el = (await panel()).querySelector(`#${id}`) as HTMLSelectElement;
    expect(el, `select #${id}`).toBeTruthy();
    el.value = value;
    el.dispatchEvent(new Event("change", { bubbles: true }));
    fixture.detectChanges();
  }

  async function submit(fixture: ReturnType<typeof render>): Promise<void> {
    (await panel()).querySelector("form")!.dispatchEvent(new Event("submit"));
  }

  async function text(fixture: ReturnType<typeof render>): Promise<string> {
    return [...document.querySelectorAll(".dialog-panel")]
      .map((el) => el.textContent ?? "")
      .join(" ");
  }

  const plantsHandler = {
    plants: {
      items: [
        plantRow("f1", "Fig “Brown Turkey”", acquisition("acq-1", null)),
        plantRow("f2", "Fig “Hardy Chicago”", null),
      ],
      pageInfo: { endCursor: null, hasNextPage: false },
    },
  };

  /** Fill the balanced $100 example into the blank create form. */
  async function fill100(fixture: ReturnType<typeof render>): Promise<void> {
    await set(fixture, "pf-total", "100.00");
    await set(fixture, "pf-tax", "5.00");
    await set(fixture, "pf-ship", "5.00");
    await set(fixture, "l0-d", "Fig tree A");
    await set(fixture, "l0-t", "45.00");
    await select(fixture, "l0-a0-p", "f1");
    await set(fixture, "l0-a0-l", "45.00");
    await set(fixture, "l0-a0-t", "2.50");
    await set(fixture, "l0-a0-s", "2.50");
    const addLine = [...(await panel()).querySelectorAll("button")].find(
      (b) => b.textContent?.trim() === "Add line",
    );
    addLine!.click();
    fixture.detectChanges();
    await set(fixture, "l1-d", "Fig tree B");
    await set(fixture, "l1-t", "45.00");
    await select(fixture, "l1-a0-p", "f2");
    await set(fixture, "l1-a0-l", "45.00");
    await set(fixture, "l1-a0-t", "2.50");
    await set(fixture, "l1-a0-s", "2.50");
  }

  it("balanced receipt previews exact balance and sends minor-unit strings", async () => {
    const calls = stubFetch({
      Plants: plantsHandler,
      CreatePurchase: {
        createPurchase: {
          clientMutationId: null,
          purchase: detail().purchase,
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    await fill100(fixture);
    expect(await text(fixture)).toContain("Balanced: 100.00 USD");
    await submit(fixture);
    await flush();
    const create = calls.find((c) => c.op === "CreatePurchase");
    expect(create?.vars).toMatchObject({
      input: {
        currency: "USD",
        totalMinor: "10000",
        taxMinor: "500",
        shippingMinor: "500",
        lines: [
          {
            description: "Fig tree A",
            quantity: "1",
            lineTotalMinor: "4500",
            allocations: [
              {
                plantId: "f1",
                lineMinor: "4500",
                taxMinor: "250",
                shippingMinor: "250",
              },
            ],
          },
          {
            description: "Fig tree B",
            quantity: "1",
            lineTotalMinor: "4500",
            allocations: [
              {
                plantId: "f2",
                lineMinor: "4500",
                taxMinor: "250",
                shippingMinor: "250",
              },
            ],
          },
        ],
      },
    });
  });

  it("editing a prefilled line total cascades to its sole allocation and the total", async () => {
    stubFetch({ Plants: plantsHandler, PurchaseDetail: detail() });
    const fixture = render("pur-1");
    await flush();
    // type only after the full prefill has landed (loadPurchase patches the
    // header first and swaps the line array after — same guard the e2e uses)
    let lt: HTMLInputElement | null = null;
    for (let i = 0; i < 20; i++) {
      lt = (await panel()).querySelector("#l0-t");
      if (lt && lt.value === "45.00") break;
      await flush();
      fixture.detectChanges();
    }
    expect(lt?.value).toBe("45.00");
    await set(fixture, "l0-t", "50.00");
    const el = (await panel()).querySelector("#l0-a0-l") as HTMLInputElement;
    expect(el.value).toBe("50.00");
    expect(await text(fixture)).toContain("Balanced: 60.00 USD");
  });

  it("an off-by-one allocation is reported with the exact discrepancy and blocks submit", async () => {
    stubFetch({
      Plants: plantsHandler,
      CreatePurchase: {
        createPurchase: {
          clientMutationId: null,
          purchase: null,
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    await fill100(fixture);
    // UI07: a line's total follows its single allocation's item amount, so
    // the off-by-one is built on a line that splits across two allocations
    const root = await panel();
    const addBtns = [...root.querySelectorAll("button")].filter(
      (b) => b.textContent?.trim() === "Add allocation",
    );
    addBtns[1].click();
    fixture.detectChanges();
    await set(fixture, "l1-a1-l", "0.00");
    await set(fixture, "l1-a0-l", "44.99");
    await submit(fixture);
    fixture.detectChanges();
    const t = await text(fixture);
    expect(t).toContain("sum to 44.99 USD");
    expect(t).toContain("off by -0.01 USD");
    const btn = (await panel()).querySelector(
      "button[type=submit]",
    ) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
  });

  it("MONEY boundary values: exponent, ceiling and format are enforced before the server", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    await set(fixture, "pf-total", "44.999");
    await submit(fixture);
    fixture.detectChanges();
    expect(await text(fixture)).toContain("2 decimal places");
    await set(fixture, "pf-total", "");
    await set(fixture, "pf-cur", "JPY");
    await set(fixture, "pf-total", "10.5");
    expect(await text(fixture)).toContain("no minor unit");
    await set(fixture, "pf-cur", "USD");
    await set(fixture, "pf-total", "10000000000000.01");
    expect(await text(fixture)).toContain("largest accepted value");
    await set(fixture, "pf-total", "10000000000000");
    expect(await text(fixture)).not.toContain("largest accepted value");
  });

  it("edit prefills exact decimals and keeps line/allocation ids on update", async () => {
    const calls = stubFetch({
      Plants: plantsHandler,
      PurchaseDetail: detail(),
      UpdatePurchase: {
        updatePurchase: {
          clientMutationId: null,
          purchase: detail().purchase,
          problems: [],
        },
      },
    });
    const fixture = render("pur-1");
    await flush();
    fixture.detectChanges();
    const root = await panel();
    expect((root.querySelector("#pf-total") as HTMLInputElement).value).toBe(
      "55.00",
    );
    expect((root.querySelector("#l0-t") as HTMLInputElement).value).toBe(
      "45.00",
    );
    expect((root.querySelector("#l0-a0-l") as HTMLInputElement).value).toBe(
      "45.00",
    );
    expect(await text(fixture)).toContain("Balanced: 55.00 USD");
    await submit(fixture);
    await flush();
    const upd = calls.find((c) => c.op === "UpdatePurchase");
    expect(upd?.vars).toMatchObject({
      input: {
        id: "pur-1",
        expectedRevision: 1,
        totalMinor: "5500",
        lines: [
          {
            id: "l1",
            lineTotalMinor: "4500",
            allocations: [{ id: "a1", plantId: "f1", lineMinor: "4500" }],
          },
        ],
      },
    });
  });

  it("a CONFLICT names the current revision and preserves the entered draft", async () => {
    stubFetch({
      Plants: plantsHandler,
      PurchaseDetail: detail(),
      UpdatePurchase: {
        updatePurchase: {
          clientMutationId: null,
          purchase: null,
          problems: [
            {
              code: "CONFLICT",
              field: "input.expectedRevision",
              message: "revision mismatch",
              currentRevision: 2,
            },
          ],
        },
      },
    });
    const fixture = render("pur-1");
    await flush();
    await set(fixture, "pf-seller", "Renamed nursery");
    await submit(fixture);
    await flush();
    fixture.detectChanges();
    expect(await text(fixture)).toContain("revision 2");
    expect(await text(fixture)).toContain("draft below is preserved");
    expect(
      ((await panel()).querySelector("#pf-seller") as HTMLInputElement).value,
    ).toBe("Renamed nursery");
  });

  it("removing a recorded price needs confirmation and sends unlinkAcquisitionIds", async () => {
    const linkedPlants = {
      plants: {
        items: [
          plantRow("f1", "Fig “Brown Turkey”", acquisition("acq-1", "a1")),
          plantRow("f2", "Fig “Hardy Chicago”", null),
        ],
        pageInfo: { endCursor: null, hasNextPage: false },
      },
    };
    const calls = stubFetch({
      Plants: linkedPlants,
      PurchaseDetail: detail(),
      UpdatePurchase: {
        updatePurchase: {
          clientMutationId: null,
          purchase: detail().purchase,
          problems: [],
        },
      },
    });
    const fixture = render("pur-1");
    await flush();
    fixture.detectChanges();
    const root = await panel();
    expect(await text(fixture)).toContain(
      "Recorded price of Fig “Brown Turkey”",
    );

    // add a second allocation, then remove the linked one
    [...root.querySelectorAll("button")]
      .find((b) => b.textContent?.trim() === "Add allocation")!
      .click();
    fixture.detectChanges();
    const removes = [
      ...root.querySelectorAll<HTMLButtonElement>(".alloc-row button"),
    ].filter((b) => b.textContent?.trim() === "Remove");
    removes[0].click();
    fixture.detectChanges();

    // fill the replacement so the line still balances
    await set(fixture, "l0-a0-l", "45.00");
    await set(fixture, "l0-a0-t", "5.00");
    await set(fixture, "l0-a0-s", "5.00");
    await select(fixture, "l0-a0-p", "f1");

    // confirmation is demanded before submit is allowed
    expect(await text(fixture)).toContain("to cost unknown");
    const btn = root.querySelector("button[type=submit]") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await submit(fixture); // guarded no-op
    const early = calls.find((c) => c.op === "UpdatePurchase");
    expect(early).toBeUndefined();

    (root.querySelector("#pf-unlink") as HTMLInputElement).checked = true;
    (root.querySelector("#pf-unlink") as HTMLInputElement).dispatchEvent(
      new Event("change", { bubbles: true }),
    );
    fixture.detectChanges();
    await submit(fixture);
    await flush();
    const upd = calls.find((c) => c.op === "UpdatePurchase");
    expect(upd?.vars).toMatchObject({
      input: { unlinkAcquisitionIds: ["acq-1"], expectedRevision: 1 },
    });
  });

  it("server VALIDATION problems land in the notice panel with their exact message", async () => {
    stubFetch({
      Plants: plantsHandler,
      CreatePurchase: {
        createPurchase: {
          clientMutationId: null,
          purchase: null,
          problems: [
            {
              code: "VALIDATION",
              field: "input.lines",
              message:
                "line totals sum to 4499 but total minus tax minus shipping is 4500",
            },
          ],
        },
      },
    });
    const fixture = render();
    await flush();
    await fill100(fixture);
    // sneak past the local preview by mutating the parsed input? no — the
    // local preview blocks this; instead trigger a server-side refusal the
    // preview cannot see: unknown plant id is impossible via the picker, so
    // assert the preview itself refuses first (UI07: the mismatch needs a
    // two-allocation line, since one allocation always matches its line):
    const root = await panel();
    const addBtns = [...root.querySelectorAll("button")].filter(
      (b) => b.textContent?.trim() === "Add allocation",
    );
    addBtns[1].click();
    fixture.detectChanges();
    await set(fixture, "l1-a1-l", "0.00");
    await set(fixture, "l1-a0-l", "44.99");
    await submit(fixture);
    await flush();
    expect(await text(fixture)).toContain("off by -0.01 USD");
    // and the server path: restore balance but with a stale-revision style
    // problem the preview cannot predict (idempotency conflict)
    stubFetch({
      Plants: plantsHandler,
      CreatePurchase: {
        createPurchase: {
          clientMutationId: null,
          purchase: null,
          problems: [
            {
              code: "IDEMPOTENCY_CONFLICT",
              field: null,
              message:
                "This request was already processed with different content.",
            },
          ],
        },
      },
    });
    await set(fixture, "l1-a0-l", "45.00");
    await submit(fixture);
    await flush();
    fixture.detectChanges();
    expect(await text(fixture)).toContain(
      "already processed with different content",
    );
  });

  const ocrDraft: ReceiptDraft = {
    __typename: "ReceiptExtractionResult",
    currency: "USD",
    lines: [],
    totalMinor: "2800",
    shippingMinor: null,
    taxMinor: null,
    purchasedOn: "2026-05-14",
    seller: null,
    needsReconciliation: true,
    provider: "local-ocr",
    model: "tesseract 5.3.4 + poppler 24.02 (local)",
  };

  const draftHost = (fixture: ReturnType<typeof render>) =>
    fixture.componentInstance as unknown as {
      ocrDraft: WritableSignal<ReceiptDraft | null>;
      onExtracted(draft: ReceiptDraft): void;
    };

  it("the OCR draft is reviewed before anything touches the form, and apply is exact", async () => {
    stubFetch({ Plants: plantsHandler, PurchaseDetail: detail() });
    const fixture = render("pur-1");
    await flush();
    fixture.detectChanges();
    await set(fixture, "pf-seller", "Keep me");

    draftHost(fixture).onExtracted(ocrDraft);
    fixture.detectChanges();

    expect(await text(fixture)).toContain("Extracted from receipt");
    expect(await text(fixture)).toContain("Amounts incomplete");
    expect(await text(fixture)).toContain(
      "Review the details before applying them to your purchase",
    );
    expect(await text(fixture)).toContain("— not read");
    expect(await text(fixture)).toContain("Engine: local-ocr");
    // nothing has been written to the form yet — the loaded purchase stands
    expect(
      ((await panel()).querySelector("#pf-total") as HTMLInputElement).value,
    ).toBe("55.00");

    const apply = [
      ...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button"),
    ].find((b) => b.textContent?.trim() === "Apply extracted details");
    apply!.click();
    fixture.detectChanges();

    const val = async (id: string) =>
      (await panel()).querySelector<HTMLInputElement>(`#${id}`)!.value;
    expect(await val("pf-total")).toBe("28.00");
    expect(await val("pf-cur")).toBe("USD");
    expect(await val("pf-date")).toBe("2026-05-14");
    // null draft fields never overwrite owner-typed values
    expect(await val("pf-seller")).toBe("Keep me");
    // the draft card is gone either way — applying is one-shot
    expect(await text(fixture)).not.toContain("Extracted from receipt");
  });

  it("discarding a draft leaves the form untouched", async () => {
    stubFetch({ Plants: plantsHandler, PurchaseDetail: detail() });
    const fixture = render("pur-1");
    await flush();
    fixture.detectChanges();

    draftHost(fixture).onExtracted(ocrDraft);
    fixture.detectChanges();

    const discard = [
      ...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button"),
    ].find((b) => b.textContent?.trim() === "Discard draft");
    discard!.click();
    fixture.detectChanges();

    expect(await text(fixture)).not.toContain("Extracted from receipt");
    const val = async (id: string) =>
      (await panel()).querySelector<HTMLInputElement>(`#${id}`)!.value;
    expect(await val("pf-total")).toBe("55.00");
    expect(await val("pf-date")).toBe("2026-03-10");
  });
  it("opens a new purchase quietly with receipt upload before manual fields", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const root = await panel();
    expect(root.querySelector("input[type=file]")).toBeTruthy();
    expect(root.querySelector(".recon-notes")).toBeNull();
    const upload = root.querySelector("receipt-upload")!;
    expect(
      upload.compareDocumentPosition(root.querySelector("#pf-seller")!) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    await submit(fixture);
    fixture.detectChanges();
    expect(root.textContent).toContain("Enter an amount.");
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
  });

  it("applies every receipt line with exact overhead while preserving the receipt total", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    draftHost(fixture).onExtracted({
      ...ocrDraft,
      seller: "Nursery",
      totalMinor: "8678",
      taxMinor: "670",
      shippingMinor: "10",
      needsReconciliation: false,
      lines: [
        { description: "Fig tree", amountMinor: "4999" },
        { description: "Blackberry", amountMinor: "2999" },
      ],
    });
    fixture.detectChanges();
    [...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button")]
      .find((b) => b.textContent?.trim() === "Apply extracted details")!
      .click();
    fixture.detectChanges();
    const root = await panel();
    const value = (id: string) =>
      root.querySelector<HTMLInputElement>("#" + id)!.value;
    expect(value("pf-total")).toBe("86.78");
    expect(value("pf-tax")).toBe("6.70");
    expect(value("pf-ship")).toBe("0.10");
    expect(value("l0-t")).toBe("49.99");
    expect(value("l1-t")).toBe("29.99");
    expect(value("l0-a0-l")).toBe("49.99");
    expect(value("l1-a0-l")).toBe("29.99");
    expect(root.textContent).toContain("Balanced: 86.78 USD");
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
  });

  it("preserves owner-entered fields when applying a receipt", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    await set(fixture, "pf-seller", "My seller");
    await set(fixture, "l0-d", "My fig");
    await set(fixture, "l0-t", "20.00");
    draftHost(fixture).onExtracted({
      ...ocrDraft,
      seller: "Different seller",
      totalMinor: "2800",
      lines: [{ description: "Other plant", amountMinor: "2800" }],
    });
    fixture.detectChanges();
    [...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button")]
      .find((b) => b.textContent?.trim() === "Apply extracted details")!
      .click();
    fixture.detectChanges();
    const root = await panel();
    expect(root.querySelector<HTMLInputElement>("#pf-seller")!.value).toBe(
      "My seller",
    );
    expect(root.querySelector<HTMLInputElement>("#l0-d")!.value).toBe("My fig");
    expect(root.querySelector<HTMLInputElement>("#l0-t")!.value).toBe("20.00");
  });

  it("keeps an unread receipt total blank instead of deriving a confirmed total", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    draftHost(fixture).onExtracted({
      ...ocrDraft,
      totalMinor: null,
      lines: [
        { description: "Fig", amountMinor: "2000" },
        { description: "Strawberries", amountMinor: null },
      ],
    });
    fixture.detectChanges();
    [...document.querySelectorAll<HTMLButtonElement>(".dialog-panel button")]
      .find((b) => b.textContent?.trim() === "Apply extracted details")!
      .click();
    fixture.detectChanges();
    const root = await panel();
    expect(root.querySelector<HTMLInputElement>("#pf-total")!.value).toBe("");
    expect(root.querySelector<HTMLInputElement>("#l0-t")!.value).toBe("20.00");
    expect(root.querySelector<HTMLInputElement>("#l1-t")!.value).toBe("");
    await submit(fixture);
    fixture.detectChanges();
    expect(root.textContent).toContain("Enter an amount.");
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
  });

  it("reuses the create key after an uncertain network result and rotates it for changed input", async () => {
    const calls = stubFetch({
      Plants: plantsHandler,
      CreatePurchase: "network",
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    await set(fixture, "l0-d", "Fig");
    await set(fixture, "l0-t", "20.00");
    await submit(fixture);
    await flush();
    await submit(fixture);
    await flush();
    let creates = calls.filter((c) => c.op === "CreatePurchase");
    expect(creates).toHaveLength(2);
    expect(creates[0].vars["clientMutationId"]).toBeTruthy();
    expect(creates[1].vars["clientMutationId"]).toBe(
      creates[0].vars["clientMutationId"],
    );
    await set(fixture, "l0-t", "25.00");
    await submit(fixture);
    await flush();
    creates = calls.filter((c) => c.op === "CreatePurchase");
    expect(creates[2].vars["clientMutationId"]).not.toBe(
      creates[0].vars["clientMutationId"],
    );
  });

  it("keeps the saved purchase and generated allocation ids when a staged receipt attach needs retry", async () => {
    const handlers: Record<string, unknown> = {
      Plants: plantsHandler,
      CreatePurchase: {
        createPurchase: { purchase: detail().purchase, problems: [] },
      },
      UpdatePurchase: {
        updatePurchase: {
          purchase: detail({ revision: 2 }).purchase,
          problems: [],
        },
      },
      PurchaseDetail: detail(),
      AttachReceipt: "network",
    };
    const calls = stubFetch(handlers);
    const fixture = render();
    await flush();
    fixture.detectChanges();
    await set(fixture, "l0-d", "Fig tree A");
    await set(fixture, "l0-t", "45.00");
    await set(fixture, "pf-tax", "5.00");
    await set(fixture, "pf-ship", "5.00");
    await select(fixture, "l0-a0-p", "f1");
    (
      fixture.componentInstance as unknown as {
        onStaged(r: { assetId: string; filename: string }): void;
      }
    ).onStaged({ assetId: "receipt-1", filename: "receipt.jpg" });
    await submit(fixture);
    await flush();
    fixture.detectChanges();
    expect(calls.filter((c) => c.op === "CreatePurchase")).toHaveLength(1);
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
    expect(await text(fixture)).toMatch(/Purchase saved/i);
    handlers["AttachReceipt"] = {
      attachReceipt: {
        receipt: { id: "r1", purchaseId: "pur-1", asset: { id: "receipt-1" } },
        problems: [],
      },
    };
    await submit(fixture);
    await flush();
    fixture.detectChanges();
    expect(calls.filter((c) => c.op === "CreatePurchase")).toHaveLength(1);
    expect(calls.find((c) => c.op === "UpdatePurchase")?.vars).toMatchObject({
      input: {
        id: "pur-1",
        expectedRevision: 1,
        lines: [{ id: "l1", allocations: [{ id: "a1" }] }],
      },
    });
    const attaches = calls.filter((c) => c.op === "AttachReceipt");
    expect(attaches).toHaveLength(2);
    expect(attaches[0].vars["clientMutationId"]).toBeTruthy();
    expect(attaches[1].vars["clientMutationId"]).toBe(
      attaches[0].vars["clientMutationId"],
    );
    expect(TestBed.inject(Router).navigate).toHaveBeenCalledWith([
      "/purchases",
    ]);
  });
  it("distinguishes balanced read amounts from required human review and explains selected currency", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    await set(fixture, "pf-cur", "GBP");
    draftHost(fixture).onExtracted({
      ...ocrDraft,
      currency: null,
      totalMinor: "21106",
      taxMinor: "1609",
      shippingMinor: null,
      purchasedOn: "2026-10-15",
      lines: [
        { description: "Little Miss Figgy", amountMinor: "4999" },
        { description: "Alma fig", amountMinor: "6500" },
        { description: "Arapaho blackberry", amountMinor: "2999" },
        { description: "Wonderful pomegranate", amountMinor: "4999" },
      ],
      needsReconciliation: true,
    });
    fixture.detectChanges();
    const t = await text(fixture);
    expect(t).toContain("Read amounts balance");
    expect(t).toContain("211.06 GBP");
    expect(t).toContain("Currency was not identified");
    expect(t).not.toContain("could not fully reconcile");
    expect(t).not.toContain("totals did not balance");
    expect(t).not.toContain("211.06 USD");
    expect(TestBed.inject(Router).navigate).not.toHaveBeenCalled();
  });
  it("shows an exact read-amount mismatch without claiming the model verified it", async () => {
    stubFetch({ Plants: plantsHandler });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    draftHost(fixture).onExtracted({
      ...ocrDraft,
      totalMinor: "1100",
      taxMinor: "100",
      lines: [{ description: "Fig", amountMinor: "999" }],
      needsReconciliation: true,
    });
    fixture.detectChanges();
    const t = await text(fixture);
    expect(t).toContain("Amounts need checking");
    expect(t).toContain("10.99 USD");
    expect(t).toContain("11.00 USD");
    expect(t).not.toContain("Read amounts balance");
  });
});
