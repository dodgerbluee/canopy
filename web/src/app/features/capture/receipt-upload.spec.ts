import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { ReceiptUpload, type ReceiptDraft } from './receipt-upload';

function stubFetch(handlers: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as {
        query: string;
        variables?: object;
      };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      let data = handlers[op];
      // an array answers sequentially (polling), repeating the last entry
      if (Array.isArray(data)) data = data.length > 1 ? data.shift() : data[0];
      if (data === 'network') throw new TypeError('fetch failed');
      return new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
}

function stubXhr(byId: Record<string, { status: number; body: string }>) {
  class FakeXHR {
    status = 0;
    responseText = '';
    withCredentials = false;
    url = '';
    private listeners: Record<string, (e?: unknown) => void> = {};
    readonly upload = { addEventListener: () => {} };
    open(_m: string, url: string): void {
      this.url = url;
    }
    setRequestHeader(): void {}
    addEventListener(ev: string, cb: (e?: unknown) => void): void {
      this.listeners[ev] = cb;
    }
    send(): void {
      const r = byId[this.url.split('/').pop() ?? ''];
      this.status = r?.status ?? 500;
      this.responseText = r?.body ?? '{}';
      this.listeners['load']();
    }
  }
  vi.stubGlobal('XMLHttpRequest', FakeXHR);
}

const asset = {
  id: 'r1',
  purpose: 'RECEIPT',
  state: 'RESERVED',
  byteCount: 4,
  mimeType: null,
  sha256: '',
  displayName: 'receipt.jpg',
  errorCode: null,
  expiresAt: '2026-11-02T00:00:00Z',
  variants: [],
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
};

describe('ReceiptUpload', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({});
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1',
      expiresAt: '2026-12-01T00:00:00Z',
      csrfToken: 'c',
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  function render(
    purchaseId: string | null = 'pur-1',
  ): ReturnType<typeof TestBed.createComponent<ReceiptUpload>> {
    const fixture = TestBed.createComponent(ReceiptUpload);
    fixture.componentRef.setInput('purchaseId', purchaseId);
    fixture.detectChanges();
    return fixture;
  }

  function pick(fixture: ReturnType<typeof TestBed.createComponent<ReceiptUpload>>): void {
    const input = fixture.nativeElement.querySelector('input[type=file]') as HTMLInputElement;
    Object.defineProperty(input, 'files', {
      value: [new File([new Uint8Array(4)], 'receipt.jpg', { type: 'image/jpeg' })],
      configurable: true,
    });
    input.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  }

  it('runs the pipeline without SubtleCrypto and finalizes using the server checksum', async () => {
    vi.stubGlobal('crypto', {
      getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto),
    });
    stubFetch({
      ReserveUpload: { reserveUpload: { asset, problems: [] } },
      FinalizeUpload: {
        finalizeUpload: {
          asset: { ...asset, state: 'UPLOADED' },
          problems: [],
        },
      },
      PendingUploads: { pendingUploads: { items: [] } },
      AttachReceipt: {
        attachReceipt: {
          receipt: { id: 'rc1', purchaseId: 'pur-1', asset },
          problems: [],
        },
      },
    });
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render();
    pick(fixture);

    await vi.waitFor(() =>
      expect((fixture.nativeElement as HTMLElement).textContent).toContain('Attached'),
    );
    const finalization = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map((c) => JSON.parse(String((c[1] as RequestInit).body)))
      .find((c) => c.query.includes('mutation FinalizeUpload'));
    expect(finalization.variables.input.sha256).toBe('a'.repeat(64));
    const vars = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(
        (c) =>
          JSON.parse(String((c[1] as RequestInit).body)) as {
            query: string;
            variables?: object;
          },
      )
      .find((c) => c.query.includes('mutation AttachReceipt'));
    expect(vars?.variables).toEqual({
      input: { purchaseId: 'pur-1', assetId: 'r1' },
    });
  });

  it('a reservation problem surfaces honestly with a retry path', async () => {
    stubFetch({
      ReserveUpload: {
        reserveUpload: {
          asset: null,
          problems: [
            {
              code: 'QUOTA_EXCEEDED',
              field: null,
              message: 'Storage quota reached.',
            },
          ],
        },
      },
      PendingUploads: { pendingUploads: { items: [] } },
    });
    const fixture = render();
    pick(fixture);

    await vi.waitFor(() =>
      expect((fixture.nativeElement as HTMLElement).textContent).toContain(
        'Storage quota reached.',
      ),
    );
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('Try again');
  });

  const runBase = {
    id: 'run-1',
    revision: 1,
    kind: 'RECEIPT_EXTRACTION',
    plantId: null,
    receiptAssetId: 'r1',
    state: 'QUEUED',
    attempts: 0,
    requestedAt: '2026-11-02T00:00:00Z',
    startedAt: null,
    finishedAt: null,
    failureCode: null,
    failureMessage: null,
    provider: null,
    model: null,
    promptVersion: null,
    resultSchemaVersion: null,
    costReservedMicroUsd: '0',
    costActualMicroUsd: null,
    evidence: {
      activityIds: [],
      measurementIds: [],
      photoIds: [],
      sourceVersionIds: [],
      weatherSnapshotIds: [],
      missingFacts: [],
      inputHash: 'h',
    },
    result: null,
  };
  const receiptResult = {
    __typename: 'ReceiptExtractionResult',
    currency: 'USD',
    lines: [{ description: 'MONSTERA DELICIOSA 6IN', amountMinor: '2200' }],
    totalMinor: '2800',
    shippingMinor: null,
    taxMinor: null,
    purchasedOn: '2026-05-14',
    seller: null,
    needsReconciliation: true,
  };

  function attachHandlers(extra: Record<string, unknown>): Record<string, unknown> {
    return {
      ReserveUpload: { reserveUpload: { asset, problems: [] } },
      FinalizeUpload: {
        finalizeUpload: {
          asset: { ...asset, state: 'UPLOADED' },
          problems: [],
        },
      },
      PendingUploads: { pendingUploads: { items: [] } },
      AttachReceipt: {
        attachReceipt: {
          receipt: { id: 'rc1', purchaseId: 'pur-1', asset },
          problems: [],
        },
      },
      ...extra,
    };
  }

  async function attach(
    fixture: ReturnType<typeof TestBed.createComponent<ReceiptUpload>>,
  ): Promise<void> {
    pick(fixture);
    await vi.waitFor(() =>
      expect((fixture.nativeElement as HTMLElement).textContent).toContain(
        'Receipt image attached.',
      ),
    );
  }

  it('offers extraction, polls the run, and emits the draft for owner review', async () => {
    stubFetch(
      attachHandlers({
        ProviderCapabilities: {
          providerCapabilities: {
            receiptExtraction: {
              available: true,
              model: null,
              externalData: false,
              unavailableReason: null,
            },
          },
        },
        RequestAnalysis: {
          requestAnalysis: {
            run: runBase,
            problems: [],
            clientMutationId: 'receipt-ocr-r1',
          },
        },
        AnalysisRunStatus: [
          { analysisRun: { ...runBase, state: 'QUEUED' } },
          {
            analysisRun: {
              ...runBase,
              state: 'SUCCEEDED',
              provider: 'local-ocr',
              model: 'tesseract 5.3.4 + poppler 24.02 (local)',
              result: receiptResult,
            },
          },
        ],
      }),
    );
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render();
    await attach(fixture);

    const drafts: {
      totalMinor: string | null;
      needsReconciliation: boolean;
    }[] = [];
    fixture.componentInstance.extracted.subscribe((d) => drafts.push(d));
    const button = Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('button'),
    ).find((b) => b.textContent?.includes('Extract details from receipt'));
    expect(button).toBeTruthy();
    button!.click();
    fixture.detectChanges();
    const dialogs = [...document.querySelectorAll<HTMLElement>('.dialog-panel')];
    expect(dialogs).toHaveLength(1);
    const confirm = [...dialogs[0].querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Extract details',
    );
    expect(confirm).toBeTruthy();
    confirm!.click();
    fixture.detectChanges();

    await vi.waitFor(
      () => expect((fixture.nativeElement as HTMLElement).textContent).toContain('Draft ready'),
      { timeout: 6000 },
    );
    const vars = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(
        (c) =>
          JSON.parse(String((c[1] as RequestInit).body)) as {
            query: string;
            variables?: object;
          },
      )
      .find((c) => c.query.includes('mutation RequestAnalysis'));
    expect(vars?.variables).toEqual({
      input: { kind: 'RECEIPT_EXTRACTION', receiptAssetId: 'r1' },
      clientMutationId: 'receipt-ocr-r1',
    });
    expect(drafts).toHaveLength(1);
    expect(drafts[0].totalMinor).toBe('2800');
    expect(drafts[0].needsReconciliation).toBe(true);
  });

  it('capability unavailable says why and never offers extraction', async () => {
    stubFetch(
      attachHandlers({
        ProviderCapabilities: {
          providerCapabilities: {
            receiptExtraction: {
              available: false,
              model: null,
              externalData: false,
              unavailableReason: 'no OCR engine on this deployment',
            },
          },
        },
      }),
    );
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render();
    await attach(fixture);

    await vi.waitFor(() =>
      expect((fixture.nativeElement as HTMLElement).textContent).toContain(
        'Text extraction is unavailable: no OCR engine on this deployment',
      ),
    );
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('complete without it');
    expect((fixture.nativeElement as HTMLElement).textContent).not.toContain(
      'Extract details from receipt',
    );
  });

  it('a failed run reports honestly and keeps the purchase intact', async () => {
    stubFetch(
      attachHandlers({
        ProviderCapabilities: {
          providerCapabilities: {
            receiptExtraction: {
              available: true,
              model: null,
              externalData: false,
              unavailableReason: null,
            },
          },
        },
        RequestAnalysis: {
          requestAnalysis: {
            run: runBase,
            problems: [],
            clientMutationId: 'receipt-ocr-r1',
          },
        },
        AnalysisRunStatus: [
          {
            analysisRun: {
              ...runBase,
              state: 'FAILED',
              failureCode: 'PROVIDER_UNAVAILABLE',
              failureMessage: null,
            },
          },
        ],
      }),
    );
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render();
    await attach(fixture);

    const button = Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('button'),
    ).find((b) => b.textContent?.includes('Extract details from receipt'));
    button!.click();
    fixture.detectChanges();
    const dialogs = [...document.querySelectorAll<HTMLElement>('.dialog-panel')];
    expect(dialogs).toHaveLength(1);
    const confirm = [...dialogs[0].querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Extract details',
    );
    expect(confirm).toBeTruthy();
    confirm!.click();

    await vi.waitFor(
      () =>
        expect((fixture.nativeElement as HTMLElement).textContent).toContain(
          'This deployment has no receipt extraction engine configured.',
        ),
      { timeout: 6000 },
    );
    expect((fixture.nativeElement as HTMLElement).textContent).toContain(
      'The saved purchase is unchanged.',
    );
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('Try extraction again');
  });
  it('uploads and extracts before any purchase exists without attaching or creating one', async () => {
    stubFetch(
      attachHandlers({
        ProviderCapabilities: {
          providerCapabilities: {
            receiptExtraction: {
              available: true,
              externalData: false,
              unavailableReason: null,
            },
          },
        },
        RequestAnalysis: {
          requestAnalysis: {
            run: { ...runBase, state: 'SUCCEEDED', result: receiptResult },
            problems: [],
          },
        },
      }),
    );
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render(null);
    pick(fixture);
    const hasOp = (name: string) =>
      (fetch as ReturnType<typeof vi.fn>).mock.calls.some((c) =>
        String((c[1] as RequestInit).body).includes('mutation ' + name + '('),
      );
    await vi.waitFor(() =>
      expect(
        [...fixture.nativeElement.querySelectorAll('button')].some(
          (b: any) => b.textContent?.trim() === 'Extract details from receipt',
        ),
      ).toBe(true),
    );
    expect(hasOp('AttachReceipt')).toBe(false);
    expect(hasOp('CreatePurchase')).toBe(false);
    expect(hasOp('RequestAnalysis')).toBe(false);
    const drafts: ReceiptDraft[] = [];
    fixture.componentInstance.extracted.subscribe((d) => drafts.push(d));
    ([...fixture.nativeElement.querySelectorAll('button')].find(
      (b: any) => b.textContent?.trim() === 'Extract details from receipt',
    ) as HTMLButtonElement)!.click();
    fixture.detectChanges();
    [...document.querySelectorAll<HTMLButtonElement>('.dialog-panel button')]
      .find((b) => b.textContent?.trim() === 'Extract details')!
      .click();
    await vi.waitFor(() => expect(drafts).toHaveLength(1));
    expect(drafts[0].totalMinor).toBe('2800');
    expect(hasOp('AttachReceipt')).toBe(false);
    expect(hasOp('CreatePurchase')).toBe(false);
  });

  it('releases busy state and allows closing and retrying a failed extraction', async () => {
    stubFetch(
      attachHandlers({
        ProviderCapabilities: {
          providerCapabilities: {
            receiptExtraction: {
              available: true,
              externalData: false,
              unavailableReason: null,
            },
          },
        },
        RequestAnalysis: {
          requestAnalysis: {
            run: {
              ...runBase,
              state: 'FAILED',
              failureMessage: 'Could not read this receipt',
            },
            problems: [],
          },
        },
      }),
    );
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render();
    const busy: boolean[] = [];
    fixture.componentInstance.busy.subscribe((value) => busy.push(value));
    await attach(fixture);
    const request = () =>
      [...fixture.nativeElement.querySelectorAll('button')].find(
        (b: any) => b.textContent?.trim() === 'Extract details from receipt',
      ) as HTMLButtonElement;
    request()!.click();
    fixture.detectChanges();
    [...document.querySelectorAll<HTMLButtonElement>('.dialog-panel button')]
      .find((b) => b.textContent?.trim() === 'Extract details')!
      .click();
    await vi.waitFor(() =>
      expect(document.body.textContent).toContain('Could not read this receipt'),
    );
    fixture.detectChanges();
    expect(busy.at(-1)).toBe(false);
    const cancel = [...document.querySelectorAll<HTMLButtonElement>('.dialog-panel button')].find(
      (b) => b.textContent?.trim() === 'Cancel',
    );
    expect(cancel).toBeTruthy();
    cancel!.click();
    fixture.detectChanges();
    expect(document.querySelector('.dialog-panel')).toBeNull();
    const retry = [...fixture.nativeElement.querySelectorAll('button')].find(
      (b: any) => b.textContent?.trim() === 'Try extraction again',
    ) as HTMLButtonElement;
    expect(retry).toBeTruthy();
    retry.click();
    fixture.detectChanges();
    const confirm = [...document.querySelectorAll<HTMLButtonElement>('.dialog-panel button')].find(
      (b) => b.textContent?.trim() === 'Extract details',
    );
    expect(confirm).toBeTruthy();
    expect(confirm!.disabled).toBe(false);
  });
  function requests() {
    return (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(
        (c) =>
          JSON.parse(String((c[1] as RequestInit).body)) as {
            query: string;
            variables: Record<string, unknown>;
          },
      )
      .filter((c) => c.query.includes('mutation RequestAnalysis('))
      .map((c) => c.variables);
  }
  async function stagedReceipt(responses: unknown[]) {
    stubFetch(
      attachHandlers({
        ProviderCapabilities: {
          providerCapabilities: {
            receiptExtraction: {
              available: true,
              externalData: false,
              unavailableReason: null,
            },
          },
        },
        RequestAnalysis: responses,
      }),
    );
    stubXhr({ r1: { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) } });
    const fixture = render(null);
    pick(fixture);
    await vi.waitFor(() =>
      expect(
        [...fixture.nativeElement.querySelectorAll('button')].some(
          (b: any) => b.textContent?.trim() === 'Extract details from receipt',
        ),
      ).toBe(true),
    );
    (
      [...fixture.nativeElement.querySelectorAll('button')].find(
        (b: any) => b.textContent?.trim() === 'Extract details from receipt',
      ) as HTMLButtonElement
    ).click();
    fixture.detectChanges();
    return fixture;
  }
  function confirmRead(fixture: ReturnType<typeof render>) {
    const buttons = [
      ...document.querySelectorAll<HTMLButtonElement>('.dialog-panel button.btn-primary'),
    ];
    expect(buttons).toHaveLength(1);
    expect(buttons[0].disabled).toBe(false);
    buttons[0].click();
    fixture.detectChanges();
  }
  const succeeded = {
    requestAnalysis: {
      run: {
        ...runBase,
        id: 'run-fresh',
        state: 'SUCCEEDED',
        result: receiptResult,
      },
      problems: [],
    },
  };

  it.each(['STALE', 'CANCELLED', 'FAILED'])(
    'explicitly retries %s with a new forced attempt, only after confirmation',
    async (state) => {
      const fixture = await stagedReceipt([
        { requestAnalysis: { run: { ...runBase, state }, problems: [] } },
        succeeded,
      ]);
      const drafts: ReceiptDraft[] = [];
      fixture.componentInstance.extracted.subscribe((d) => drafts.push(d));
      confirmRead(fixture);
      await vi.waitFor(() =>
        expect(document.body.textContent).toContain(
          'Your receipt and purchase draft are still here.',
        ),
      );
      fixture.detectChanges();
      expect(requests()).toHaveLength(1);
      expect(drafts).toHaveLength(0);
      expect(document.body.textContent).not.toContain('purchase is complete');
      expect(document.body.textContent).toContain('starts a new read');
      [...document.querySelectorAll<HTMLButtonElement>('.dialog-panel button')]
        .find((b) => b.textContent?.trim() === 'Cancel')!
        .click();
      fixture.detectChanges();
      (
        [...fixture.nativeElement.querySelectorAll('button')].find(
          (b: any) => b.textContent?.trim() === 'Try extraction again',
        ) as HTMLButtonElement
      ).click();
      fixture.detectChanges();
      expect(requests()).toHaveLength(1);
      confirmRead(fixture);
      await vi.waitFor(() => expect(drafts).toHaveLength(1));
      expect(requests()[0]).toEqual({
        input: { kind: 'RECEIPT_EXTRACTION', receiptAssetId: 'r1' },
        clientMutationId: 'receipt-ocr-r1',
      });
      expect(requests()[1]['input']).toEqual({
        kind: 'RECEIPT_EXTRACTION',
        receiptAssetId: 'r1',
        forceRerun: true,
      });
      expect(requests()[1]['clientMutationId']).not.toBe(requests()[0]['clientMutationId']);
    },
  );
  it('reuses the exact initial attempt after an uncertain response', async () => {
    const fixture = await stagedReceipt(['network', succeeded]);
    const drafts: ReceiptDraft[] = [];
    fixture.componentInstance.extracted.subscribe((d) => drafts.push(d));
    confirmRead(fixture);
    await vi.waitFor(() =>
      expect(document.body.textContent).toContain('Could not reach the server'),
    );
    fixture.detectChanges();
    confirmRead(fixture);
    await vi.waitFor(() => expect(drafts).toHaveLength(1));
    expect(requests()).toHaveLength(2);
    expect(requests()[1]).toEqual(requests()[0]);
    expect(requests()[1]['input']).not.toHaveProperty('forceRerun');
  });
  it('reuses the exact forced attempt after its response is uncertain', async () => {
    const fixture = await stagedReceipt([
      {
        requestAnalysis: { run: { ...runBase, state: 'STALE' }, problems: [] },
      },
      'network',
      succeeded,
    ]);
    const drafts: ReceiptDraft[] = [];
    fixture.componentInstance.extracted.subscribe((d) => drafts.push(d));
    confirmRead(fixture);
    await vi.waitFor(() =>
      expect(document.body.textContent).toContain('previous read was invalidated'),
    );
    fixture.detectChanges();
    confirmRead(fixture);
    await vi.waitFor(() =>
      expect(document.body.textContent).toContain('Could not reach the server'),
    );
    fixture.detectChanges();
    confirmRead(fixture);
    await vi.waitFor(() => expect(drafts).toHaveLength(1));
    expect(requests()).toHaveLength(3);
    expect(requests()[2]).toEqual(requests()[1]);
    expect(requests()[1]['input']).toHaveProperty('forceRerun', true);
    expect(requests()[1]['clientMutationId']).not.toBe(requests()[0]['clientMutationId']);
  });
  it.each([false, true])(
    'does not apply a run carried by a non-idempotency problem (mixed=%s)',
    async (mixed) => {
      const fixture = await stagedReceipt([
        {
          requestAnalysis: {
            run: succeeded.requestAnalysis.run,
            problems: [
              ...(mixed
                ? [
                    {
                      code: 'IDEMPOTENCY_CONFLICT',
                      message: 'Reuse existing run',
                    },
                  ]
                : []),
              { code: 'RATE_LIMITED', message: 'Daily limit reached' },
            ],
          },
        },
      ]);
      const drafts: ReceiptDraft[] = [];
      fixture.componentInstance.extracted.subscribe((d) => drafts.push(d));
      confirmRead(fixture);
      await vi.waitFor(() => expect(document.body.textContent).toContain('Daily limit reached'));
      expect(drafts).toHaveLength(0);
    },
  );
});
