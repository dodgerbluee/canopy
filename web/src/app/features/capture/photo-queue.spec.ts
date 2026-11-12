import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { PhotoQueue } from './photo-queue';

function asset(over: Record<string, unknown> = {}) {
  return {
    id: 'a1',
    purpose: 'PHOTO',
    state: 'RESERVED',
    byteCount: 10,
    mimeType: null,
    sha256: '',
    displayName: 'one.jpg',
    errorCode: null,
    expiresAt: '2026-11-02T00:00:00Z',
    variants: [],
    createdAt: '2026-11-01T00:00:00Z',
    updatedAt: '2026-11-01T00:00:00Z',
    ...over,
  };
}

const photo = {
  id: 'ph1',
  plantId: 'p1',
  takenAt: null,
  dateSource: 'UPLOAD_FALLBACK',
  timezone: null,
  caption: null,
  viewpoint: 'WHOLE',
  isCover: false,
  revision: 1,
  deletedAt: null,
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
  asset: asset({ state: 'READY' }),
};

/** Stateful GraphQL transport: handlers may be values or call-indexed fns. */
function stubFetch(handlers: Record<string, unknown | ((n: number, vars: object) => unknown)>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  const counts: Record<string, number> = {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as {
        query: string;
        variables?: object;
      };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      counts[op] = (counts[op] ?? 0) + 1;
      calls.push({
        op,
        vars: (body.variables ?? {}) as Record<string, unknown>,
      });
      const h = handlers[op];
      const data = typeof h === 'function' ? h(counts[op], body.variables ?? {}) : h;
      return new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

/** XHR double keyed by asset id so per-file outcomes are deterministic. */
function stubXhr(byId: Record<string, { status: number; body: string }>) {
  class FakeXHR {
    status = 0;
    responseText = '';
    withCredentials = false;
    url = '';
    private listeners: Record<string, (e?: unknown) => void> = {};
    private progressCb: ((e: unknown) => void) | null = null;
    readonly upload = {
      addEventListener: (_ev: string, cb: (e: unknown) => void) => {
        this.progressCb = cb;
      },
    };
    open(_m: string, url: string): void {
      this.url = url;
    }
    setRequestHeader(): void {}
    addEventListener(ev: string, cb: (e?: unknown) => void): void {
      this.listeners[ev] = cb;
    }
    send(): void {
      const id = this.url.split('/').pop() ?? '';
      const r = byId[id] ?? {
        status: 500,
        body: JSON.stringify({
          problems: [{ code: 'X', message: 'unexpected' }],
        }),
      };
      this.progressCb?.({ lengthComputable: true, loaded: 5, total: 10 });
      this.status = r.status;
      this.responseText = r.body;
      this.listeners['load']();
    }
  }
  vi.stubGlobal('XMLHttpRequest', FakeXHR);
}

const okPut = { status: 200, body: JSON.stringify({ sha256: 'a'.repeat(64) }) };

function jpg(name: string, bytes = 10): File {
  return new File([new Uint8Array(bytes)], name, { type: 'image/jpeg' });
}

function render(plantId: string | null): ReturnType<typeof TestBed.createComponent<PhotoQueue>> {
  const fixture = TestBed.createComponent(PhotoQueue);
  fixture.componentRef.setInput('plantId', plantId);
  fixture.detectChanges();
  return fixture;
}

function pick(
  fixture: ReturnType<typeof TestBed.createComponent<PhotoQueue>>,
  files: File[],
  nth = 0,
): void {
  const input = (fixture.nativeElement as HTMLElement).querySelectorAll('input[type=file]')[
    nth
  ] as HTMLInputElement;
  Object.defineProperty(input, 'files', { value: files, configurable: true });
  input.dispatchEvent(new Event('change'));
  fixture.detectChanges();
}

const text = (fixture: ReturnType<typeof TestBed.createComponent<PhotoQueue>>) =>
  (fixture.nativeElement as HTMLElement).textContent ?? '';

describe('PhotoQueue', () => {
  let createObjectURL: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    // Model the actual Android LAN origin: Web Crypto's secure-only APIs
    // are absent for both fresh uploads and reservation recovery.
    vi.stubGlobal('crypto', {
      getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto),
    });
    createObjectURL = vi.fn(() => 'blob:preview');
    URL.createObjectURL = createObjectURL as unknown as (obj: Blob | MediaSource) => string;
    URL.revokeObjectURL = vi.fn();
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

  it('uploads original bytes and asks the server to prefer capture metadata', async () => {
    stubFetch({
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: { reserveUpload: { asset: asset(), problems: [] } },
      FinalizeUpload: {
        finalizeUpload: { asset: asset({ state: 'UPLOADED' }), problems: [] },
      },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({ a1: okPut });
    const fixture = render('p1');
    expect(fixture.componentInstance.busy()).toBe(false);
    expect(fixture.componentInstance.dirty()).toBe(false);
    pick(fixture, [jpg('one.jpg')]);
    expect(fixture.componentInstance.busy()).toBe(true);
    expect(fixture.componentInstance.dirty()).toBe(true);

    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    expect(fixture.componentInstance.busy()).toBe(false);
    expect(fixture.componentInstance.dirty()).toBe(false);
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => {
      const b = JSON.parse(String((c[1] as RequestInit).body)) as {
        query: string;
        variables?: object;
      };
      return {
        op: /(?:query|mutation)\s+(\w+)/.exec(b.query)?.[1],
        vars: b.variables,
      };
    });
    const attach = calls.find((c) => c.op === 'AttachPhoto');
    expect(calls.find((c) => c.op === 'FinalizeUpload')?.vars).toEqual({
      input: { assetId: 'a1', sha256: 'a'.repeat(64) },
    });
    expect(attach?.vars).toEqual({
      input: {
        plantId: 'p1',
        assetId: 'a1',
        viewpoint: 'WHOLE',
        dateSource: 'UPLOAD_FALLBACK',
        preferCaptureDate: true,
        takenAt: null,
        timezone: 'UTC',
        caption: null,
        isCover: false,
      },
    });
  });

  it('a new photo leaves cover selection automatic unless explicitly chosen', async () => {
    stubFetch({
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: { reserveUpload: { asset: asset(), problems: [] } },
      FinalizeUpload: {
        finalizeUpload: { asset: asset({ state: 'UPLOADED' }), problems: [] },
      },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({ a1: okPut });
    const fixture = render('p1');
    fixture.componentRef.setInput('hasCover', true);
    pick(fixture, [jpg('one.jpg')]);
    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => {
      const b = JSON.parse(String((c[1] as RequestInit).body)) as {
        query: string;
        variables?: object;
      };
      return {
        op: /(?:query|mutation)\s+(\w+)/.exec(b.query)?.[1],
        vars: b.variables,
      };
    });
    expect(calls.find((c) => c.op === 'AttachPhoto')?.vars).toMatchObject({
      input: { isCover: false },
    });
  });

  it('an owner-stated date travels as USER with the browser timezone', async () => {
    // poll 1 = recover (empty), poll 2 = still processing, poll 3 = gone
    stubFetch({
      PendingUploads: (n: number) => ({
        pendingUploads: {
          items: n === 2 ? [asset({ state: 'PROCESSING' })] : [],
        },
      }),
      ReserveUpload: { reserveUpload: { asset: asset(), problems: [] } },
      FinalizeUpload: {
        finalizeUpload: { asset: asset({ state: 'UPLOADED' }), problems: [] },
      },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({ a1: okPut });
    const fixture = render('p1');
    pick(fixture, [jpg('one.jpg')]);

    await vi.waitFor(() => expect(text(fixture)).toContain('Processing'));
    const date = fixture.nativeElement.querySelector('input[type=date]') as HTMLInputElement;
    date.value = '2026-10-01';
    date.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'), {
      timeout: 10_000,
    });
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => {
      const b = JSON.parse(String((c[1] as RequestInit).body)) as {
        query: string;
        variables?: object;
      };
      return {
        op: /(?:query|mutation)\s+(\w+)/.exec(b.query)?.[1],
        vars: b.variables,
      };
    });
    const attach = calls.find((c) => c.op === 'AttachPhoto');
    const input = (attach?.vars as { input: Record<string, unknown> }).input;
    expect(input['dateSource']).toBe('USER');
    expect(input['takenAt']).toBe('2026-10-01T00:00:00.000Z'); // container TZ is UTC
    expect(input['timezone']).toBe('UTC');
  });

  it('a checksum mismatch re-reserves once and still attaches', async () => {
    stubFetch({
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: (n: number) => ({
        reserveUpload: {
          asset: asset({ id: n === 1 ? 'a1' : 'a2' }),
          problems: [],
        },
      }),
      FinalizeUpload: (n: number) =>
        n === 1
          ? {
              finalizeUpload: {
                asset: null,
                problems: [
                  {
                    code: 'VALIDATION',
                    field: null,
                    message: 'Checksum does not match.',
                  },
                ],
              },
            }
          : {
              finalizeUpload: {
                asset: asset({ id: 'a2', state: 'UPLOADED' }),
                problems: [],
              },
            },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({ a1: okPut, a2: okPut });
    const fixture = render('p1');
    pick(fixture, [jpg('one.jpg')]);

    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    const ops = (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) => {
      const b = JSON.parse(String((c[1] as RequestInit).body)) as {
        query: string;
      };
      return /(?:query|mutation)\s+(\w+)/.exec(b.query)?.[1];
    });
    expect(ops.filter((o) => o === 'ReserveUpload')).toHaveLength(2);
    const attachVars = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(
        (c) =>
          JSON.parse(String((c[1] as RequestInit).body)) as {
            query: string;
            variables?: object;
          },
      )
      .find((c) => c.query.includes('mutation AttachPhoto'));
    expect((attachVars?.variables as { input: { assetId: string } }).input.assetId).toBe('a2');
  });

  it('does not finalize or attach when the upload receipt has no checksum', async () => {
    stubFetch({
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: { reserveUpload: { asset: asset(), problems: [] } },
    });
    stubXhr({ a1: { status: 200, body: '{}' } });
    const fixture = render('p1');
    pick(fixture, [jpg('existing-photo.jpg')]);
    await vi.waitFor(() => expect(text(fixture)).toContain('invalid upload checksum'));
    const bodies = (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) =>
      String((c[1] as RequestInit).body),
    );
    expect(bodies.some((b) => b.includes('mutation FinalizeUpload'))).toBe(false);
    expect(bodies.some((b) => b.includes('mutation AttachPhoto'))).toBe(false);
    expect(fixture.componentInstance.busy()).toBe(false);
  });

  it('one bad file fails alone; its sibling still attaches', async () => {
    stubFetch({
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: (n: number) => ({
        reserveUpload: { asset: asset({ id: `a${n}` }), problems: [] },
      }),
      FinalizeUpload: (n: number) => ({
        finalizeUpload: {
          asset: asset({ id: `a${n}`, state: 'UPLOADED' }),
          problems: [],
        },
      }),
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({
      a1: {
        status: 415,
        body: JSON.stringify({
          problems: [
            {
              code: 'UNSUPPORTED_MEDIA_TYPE',
              message: 'Content is not an allowed image type.',
            },
          ],
        }),
      },
      a2: okPut,
    });
    const fixture = render('p1');
    pick(fixture, [jpg('one.jpg'), jpg('two.jpg')]);

    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    await vi.waitFor(() => expect(text(fixture)).toContain('Failed'));
    expect(text(fixture)).toContain('Content is not an allowed image type.');
    expect(text(fixture)).toContain('Retry');
    expect(fixture.componentInstance.busy()).toBe(false);
    expect(fixture.componentInstance.dirty()).toBe(true);
  });

  it('reload recovery: a fully received upload finalizes without reselection', async () => {
    stubFetch({
      // first poll (recover) shows it UPLOADED; later polls show it gone
      PendingUploads: (n: number) => ({
        pendingUploads: {
          items:
            n === 1
              ? [
                  asset({
                    id: 'a9',
                    state: 'UPLOADED',
                    sha256: 'cafe',
                    displayName: 'lost.jpg',
                    byteCount: 10,
                  }),
                ]
              : [],
        },
      }),
      FinalizeUpload: {
        finalizeUpload: {
          asset: asset({ id: 'a9', state: 'UPLOADED' }),
          problems: [],
        },
      },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    const fixture = render('p1');

    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    const finVars = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(
        (c) =>
          JSON.parse(String((c[1] as RequestInit).body)) as {
            query: string;
            variables?: object;
          },
      )
      .find((c) => c.query.includes('mutation FinalizeUpload'));
    expect(finVars?.variables).toEqual({
      input: { assetId: 'a9', sha256: 'cafe' },
    });
  });

  it('reload recovery: an incomplete upload asks for the file and checks its size', async () => {
    stubFetch({
      PendingUploads: (n: number) => ({
        pendingUploads: {
          items:
            n === 1
              ? [
                  asset({
                    id: 'a8',
                    state: 'RESERVED',
                    displayName: 'half.jpg',
                    byteCount: 10,
                  }),
                ]
              : [],
        },
      }),
      ReserveUpload: {
        reserveUpload: { asset: asset({ id: 'a8' }), problems: [] },
      },
      FinalizeUpload: {
        finalizeUpload: {
          asset: asset({ id: 'a8', state: 'UPLOADED' }),
          problems: [],
        },
      },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({ a8: okPut });
    const fixture = render('p1');

    await vi.waitFor(() => expect(text(fixture)).toContain('Needs file'));
    expect(text(fixture)).toContain('Reselect file');

    // wrong size → honest refusal, no PUT happens
    const re = (fixture.nativeElement as HTMLElement).querySelectorAll(
      'input[type=file]',
    )[1] as HTMLInputElement;
    Object.defineProperty(re, 'files', {
      value: [jpg('wrong.jpg', 11)],
      configurable: true,
    });
    re.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    await vi.waitFor(() => expect(text(fixture)).toContain('did not match its reservation size'));

    // right size → resumes on the same reservation
    Object.defineProperty(re, 'files', {
      value: [jpg('half.jpg', 10)],
      configurable: true,
    });
    re.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    const finVars = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(
        (c) =>
          JSON.parse(String((c[1] as RequestInit).body)) as {
            query: string;
            variables?: object;
          },
      )
      .find((c) => c.query.includes('mutation FinalizeUpload'));
    expect((finVars?.variables as { input: { assetId: string } }).input.assetId).toBe('a8');
  });

  it('workspace scope waits for a plant pick, then attaches on selection', async () => {
    stubFetch({
      Plants: {
        plants: {
          items: [{ id: 'p9', name: 'Pothos' }],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: { reserveUpload: { asset: asset(), problems: [] } },
      FinalizeUpload: {
        finalizeUpload: { asset: asset({ state: 'UPLOADED' }), problems: [] },
      },
      AttachPhoto: { attachPhoto: { photo, problems: [] } },
    });
    stubXhr({ a1: okPut });
    const fixture = render(null);
    pick(fixture, [jpg('one.jpg')]);

    await vi.waitFor(() => expect(text(fixture)).toContain('Choose a plant'));
    expect(fixture.componentInstance.busy()).toBe(false);
    expect(fixture.componentInstance.dirty()).toBe(true);
    // no attach attempted yet
    const before = (fetch as ReturnType<typeof vi.fn>).mock.calls.filter((c) =>
      String(c[1]).includes('mutation AttachPhoto'),
    );
    expect(before).toHaveLength(0);

    const sel = fixture.nativeElement.querySelector('select') as HTMLSelectElement;
    sel.value = 'p9';
    sel.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    expect(fixture.componentInstance.busy()).toBe(true);
    await vi.waitFor(() => expect(text(fixture)).toContain('Attached'));
    expect(fixture.componentInstance.busy()).toBe(false);
    expect(fixture.componentInstance.dirty()).toBe(false);
  });

  it('Remove drops the row and revokes its preview', async () => {
    stubFetch({
      PendingUploads: () => ({ pendingUploads: { items: [] } }),
      ReserveUpload: () => new Promise(() => {}), // never resolves: item stays in-flight
    });
    const fixture = render('p1');
    pick(fixture, [jpg('one.jpg')]);
    await vi.waitFor(() => expect(text(fixture)).toContain('one.jpg'));
    const revoke = URL.revokeObjectURL as ReturnType<typeof vi.fn>;
    (
      (fixture.nativeElement as HTMLElement).querySelector('button.btn-danger') as HTMLElement
    ).click();
    fixture.detectChanges();
    expect(text(fixture)).not.toContain('one.jpg');
    expect(revoke).toHaveBeenCalled();
  });
});
