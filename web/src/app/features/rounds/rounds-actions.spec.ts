import { signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RecordActivityDocument, AttachPhotoDocument } from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient } from '../../core/graphql/client';
import { Uploader } from '../capture/upload';
import { localDateInput } from '../journal/journal-view';
import { RoundActions } from './rounds-actions';

const now = new Date('2026-11-05T02:15:00.000Z');
const ready = { ok: true, asset: { id: 'asset-a', state: 'READY' } };
const reserved = { ok: true, asset: { id: 'asset-a', state: 'RESERVED' } };
const waterOK = {
  kind: 'ok',
  errors: [],
  data: { recordActivity: { activity: { id: 'water-a' }, problems: [] } },
};
const photoOK = {
  kind: 'ok',
  errors: [],
  data: {
    attachPhoto: {
      photo: { id: 'photo-a', takenAt: '2026-10-31T05:30:39.125Z', dateSource: 'EXIF' },
      problems: [],
    },
  },
};
const file = () => new File(['plant image'], 'tree.jpg', { type: 'image/jpeg' });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}
async function settle() {
  for (let i = 0; i < 12; i++) await Promise.resolve();
}

describe('RoundActions', () => {
  let actions: RoundActions;
  let gql: { mutate: ReturnType<typeof vi.fn> };
  let uploader: {
    reserve: ReturnType<typeof vi.fn>;
    put: ReturnType<typeof vi.fn>;
    finalize: ReturnType<typeof vi.fn>;
    waitTerminal: ReturnType<typeof vi.fn>;
  };
  let session: {
    info: ReturnType<
      typeof signal<{ workspaceId: string; csrfToken: string; expiresAt: string } | null>
    >;
    state: ReturnType<typeof signal<string>>;
  };
  const BaseURL = URL;
  beforeEach(() => {
    session = {
      info: signal({ workspaceId: 'workspace-a', csrfToken: 'fixture', expiresAt: '2099-01-01' }),
      state: signal('authed'),
    };
    gql = {
      mutate: vi.fn(async (doc: unknown) =>
        doc === RecordActivityDocument ? structuredClone(waterOK) : structuredClone(photoOK),
      ),
    };
    uploader = {
      reserve: vi.fn(async () => reserved),
      put: vi.fn(async () => ({ ok: true, sha256: 'a'.repeat(64) })),
      finalize: vi.fn(async () => ready),
      waitTerminal: vi.fn(async () => ({ outcome: 'gone' })),
    };
    vi.stubGlobal(
      'URL',
      class extends BaseURL {
        static override createObjectURL = vi.fn(() => 'blob:round-fixture');
        static override revokeObjectURL = vi.fn();
      },
    );
    TestBed.configureTestingModule({
      providers: [
        RoundActions,
        { provide: GraphQLClient, useValue: gql },
        { provide: Uploader, useValue: uploader },
        { provide: SessionService, useValue: session },
      ],
    });
    actions = TestBed.inject(RoundActions);
    TestBed.tick();
  });
  afterEach(() => {
    TestBed.resetTestingModule();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('records WATER for the exact plant/time without inventing a volume and blocks repeated taps', async () => {
    await actions.markWatered('lemon', now);
    await actions.markWatered('lemon', now);
    expect(gql.mutate).toHaveBeenCalledTimes(1);
    const [doc, vars] = gql.mutate.mock.calls[0];
    expect(doc).toBe(RecordActivityDocument);
    expect(vars.input).toEqual({
      plantId: 'lemon',
      kind: 'WATER',
      occurredAt: now.toISOString(),
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      quantity: null,
      unit: null,
      product: null,
      note: null,
    });
    expect(vars.clientMutationId).toEqual(expect.any(String));
    expect(vars.clientMutationId.length).toBeGreaterThan(20);
    expect(actions.waters()[0]).toMatchObject({
      plantId: 'lemon',
      dayKey: localDateInput(now.toISOString()),
      state: 'saved',
      activityId: 'water-a',
    });
    expect(actions.hasUnfinished()).toBe(false);
  });
  it('holds the same watering intent while in flight and retries an uncertain save with identical payload/key', async () => {
    const pending = deferred<any>();
    gql.mutate.mockReturnValueOnce(pending.promise);
    const save = actions.markWatered('fig', now);
    await actions.markWatered('fig', now);
    expect(gql.mutate).toHaveBeenCalledTimes(1);
    expect(actions.waters()[0].state).toBe('saving');
    pending.resolve({ kind: 'network' });
    await save;
    expect(actions.waters()[0].state).toBe('failed');
    expect(actions.hasUnfinished()).toBe(true);
    const original = structuredClone(gql.mutate.mock.calls[0][1]);
    await actions.retryWater(actions.waters()[0].id);
    expect(gql.mutate.mock.calls[1][1]).toEqual(original);
    expect(actions.waters()[0].state).toBe('saved');
  });
  it('allows distinct plants and a new local calendar day without reusing their intents', async () => {
    await actions.markWatered('fig', new Date(2026, 10, 4, 23, 59));
    await actions.markWatered('lime', new Date(2026, 10, 4, 23, 59));
    await actions.markWatered('fig', new Date(2026, 10, 5, 0, 1));
    expect(gql.mutate).toHaveBeenCalledTimes(3);
    expect(new Set(gql.mutate.mock.calls.map((c) => c[1].clientMutationId)).size).toBe(3);
  });
  it.each([
    { kind: 'ok', errors: [{ message: 'Resolver failed' }], data: null },
    {
      kind: 'ok',
      errors: [],
      data: {
        recordActivity: {
          activity: null,
          problems: [{ message: 'Plant unavailable', code: 'NOT_FOUND' }],
        },
      },
    },
    { kind: 'ok', errors: [], data: { recordActivity: { activity: null, problems: [] } } },
  ])('never reports watering saved from an error or empty payload', async (result) => {
    gql.mutate.mockResolvedValueOnce(result);
    await actions.markWatered('fig', now);
    expect(actions.waters()[0].state).toBe('failed');
    expect(actions.waters()[0].error).toBeTruthy();
  });
  it('saves a photo to the captured target and explicit today, reusing the existing upload transport', async () => {
    await actions.takePhoto('lemon', file(), now);
    expect(uploader.reserve).toHaveBeenCalledWith('PHOTO', 'tree.jpg', 11, expect.any(String));
    expect(uploader.put).toHaveBeenCalledTimes(1);
    expect(uploader.finalize).toHaveBeenCalledWith('asset-a', 'a'.repeat(64), expect.any(String));
    expect(uploader.waitTerminal).not.toHaveBeenCalled();
    const call = gql.mutate.mock.calls.find((c) => c[0] === AttachPhotoDocument)!;
    expect(call[1].input).toEqual({
      plantId: 'lemon',
      assetId: 'asset-a',
      takenAt: null,
      preferCaptureDate: true,
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      dateSource: 'UPLOAD_FALLBACK',
      viewpoint: 'WHOLE',
      isCover: false,
      caption: null,
    });
    expect(actions.photos()[0]).toMatchObject({
      plantId: 'lemon',
      state: 'saved',
      photoId: 'photo-a',
      takenAt: '2026-10-31T05:30:39.125Z',
      dateSource: 'EXIF',
      file: null,
    });
    expect(URL.revokeObjectURL).toHaveBeenCalled();
  });
  it('leaves cover choice to the server quality policy', async () => {
    await actions.takePhoto('lemon', file(), now);
    const call = gql.mutate.mock.calls.find((c) => c[0] === AttachPhotoDocument)!;
    expect(call[1].input).toMatchObject({ isCover: false });
  });
  it('keeps a failed reserve idempotent and blocks a second photo while that intent remains unfinished', async () => {
    uploader.reserve.mockResolvedValueOnce({
      ok: false,
      code: 'NETWORK',
      message: 'Unknown result',
    });
    await actions.takePhoto('fig', file(), now);
    await actions.takePhoto('fig', file(), now);
    expect(actions.photos()).toHaveLength(1);
    expect(uploader.reserve).toHaveBeenCalledTimes(1);
    const first = [...uploader.reserve.mock.calls[0]];
    await actions.retryPhoto(actions.photos()[0].id);
    expect(uploader.reserve.mock.calls[1]).toEqual(first);
    expect(actions.photos()[0].state).toBe('saved');
  });
  it('retries an uncertain attachment without uploading again and preserves its original timestamp/key', async () => {
    gql.mutate.mockResolvedValueOnce({ kind: 'network' });
    await actions.takePhoto('fig', file(), now);
    const job = actions.photos()[0];
    expect(job).toMatchObject({ state: 'failed', phase: 'attach' });
    const vars = structuredClone(gql.mutate.mock.calls[0][1]);
    await actions.retryPhoto(job.id);
    expect(gql.mutate.mock.calls[1][1]).toEqual(vars);
    expect(uploader.reserve).toHaveBeenCalledTimes(1);
    expect(uploader.put).toHaveBeenCalledTimes(1);
    expect(uploader.finalize).toHaveBeenCalledTimes(1);
    expect(actions.photos()[0].state).toBe('saved');
  });
  it('resumes finalization after a lost response without repeating the upload', async () => {
    uploader.finalize.mockResolvedValueOnce({
      ok: false,
      code: 'NETWORK',
      message: 'Unknown finalization',
    });
    await actions.takePhoto('fig', file(), now);
    expect(actions.photos()[0]).toMatchObject({ state: 'failed', phase: 'finalize' });
    const vars = [...uploader.finalize.mock.calls[0]];
    await actions.retryPhoto(actions.photos()[0].id);
    expect(uploader.finalize.mock.calls[1]).toEqual(vars);
    expect(uploader.put).toHaveBeenCalledTimes(1);
  });
  it('leaves processing timeouts retryable without re-uploading or prematurely attaching', async () => {
    uploader.finalize.mockResolvedValue({
      ok: true,
      asset: { id: 'asset-a', state: 'PROCESSING' },
    });
    uploader.waitTerminal
      .mockResolvedValueOnce({ outcome: 'timeout' })
      .mockResolvedValueOnce({ outcome: 'gone' });
    await actions.takePhoto('fig', file(), now);
    expect(actions.photos()[0]).toMatchObject({ state: 'failed', phase: 'process' });
    expect(gql.mutate).not.toHaveBeenCalled();
    await actions.retryPhoto(actions.photos()[0].id);
    expect(actions.photos()[0].state).toBe('saved');
    expect(uploader.reserve).toHaveBeenCalledTimes(1);
  });
  it('refuses an absent checksum and never finalizes unknown bytes', async () => {
    uploader.put.mockResolvedValue({ ok: true, sha256: '' });
    await actions.takePhoto('fig', file(), now);
    expect(actions.photos()[0].state).toBe('failed');
    expect(uploader.finalize).not.toHaveBeenCalled();
    expect(gql.mutate).not.toHaveBeenCalled();
  });
  it('does not claim an attachment when the server refuses or omits the photo', async () => {
    gql.mutate.mockResolvedValue({
      kind: 'ok',
      errors: [],
      data: {
        attachPhoto: { photo: null, problems: [{ code: 'NOT_FOUND', message: 'Asset expired' }] },
      },
    });
    await actions.takePhoto('fig', file(), now);
    expect(actions.photos()[0].state).toBe('failed');
    expect(actions.photos()[0].photoId).toBeNull();
  });
  it('keeps independent target plants correct when a slow photo finishes after another', async () => {
    const slow = deferred<any>();
    uploader.put.mockReturnValueOnce(slow.promise);
    const first = actions.takePhoto('lemon', file(), now);
    await settle();
    await actions.takePhoto('lime', file(), now);
    slow.resolve({ ok: true, sha256: 'a'.repeat(64) });
    await first;
    expect(gql.mutate.mock.calls.map((c) => c[1].input.plantId)).toEqual(['lime', 'lemon']);
    expect(actions.photos().every((j) => j.state === 'saved')).toBe(true);
  });
  it('does not continue an old photo into a new workspace after an awaited stage', async () => {
    const pending = deferred<any>();
    uploader.reserve.mockReturnValueOnce(pending.promise);
    const save = actions.takePhoto('fig', file(), now);
    session.info.set({ ...session.info()!, workspaceId: 'workspace-b' });
    TestBed.tick();
    pending.resolve(reserved);
    await save;
    expect(uploader.put).not.toHaveBeenCalled();
    expect(gql.mutate).not.toHaveBeenCalled();
    expect(actions.photos()).toHaveLength(0);
  });
  it('releases a deliberately discarded failed photo without deleting server records', async () => {
    uploader.put.mockResolvedValueOnce({ ok: false, code: 'NETWORK', message: 'Lost connection' });
    await actions.takePhoto('fig', file(), now);
    const id = actions.photos()[0].id;
    actions.discardPhoto(id);
    expect(actions.photos()).toHaveLength(0);
    expect(URL.revokeObjectURL).toHaveBeenCalled();
    expect(gql.mutate).not.toHaveBeenCalled();
  });
  it('protects an in-flight photo from accidental discard and a browser reload', async () => {
    const pending = deferred<any>();
    uploader.reserve.mockReturnValueOnce(pending.promise);
    const save = actions.takePhoto('fig', file(), now);
    actions.discardPhoto(actions.photos()[0].id);
    expect(actions.photos()).toHaveLength(1);
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    pending.resolve(reserved);
    await save;
    expect(actions.hasUnfinished()).toBe(false);
  });
  it('clears completed and failed jobs on a workspace switch without depending on the page', async () => {
    await actions.markWatered('fig', now);
    session.info.set({ ...session.info()!, workspaceId: 'workspace-b' });
    TestBed.tick();
    expect(actions.waters()).toHaveLength(0);
  });
  it('rejects a late callback after switching away from and back to the same workspace', async () => {
    const pending = deferred<any>();
    uploader.reserve.mockReturnValueOnce(pending.promise);
    const save = actions.takePhoto('fig', file(), now);
    session.info.set({ ...session.info()!, workspaceId: 'workspace-b' });
    TestBed.tick();
    session.info.set({ ...session.info()!, workspaceId: 'workspace-a' });
    TestBed.tick();
    pending.resolve(reserved);
    await save;
    expect(uploader.put).not.toHaveBeenCalled();
    expect(actions.photos()).toHaveLength(0);
  });
  it('keeps an unexpected upload exception retryable and blocks duplicate in-flight photo taps', async () => {
    const pending = deferred<any>();
    uploader.reserve.mockReturnValueOnce(pending.promise);
    const save = actions.takePhoto('fig', file(), now);
    await actions.takePhoto('fig', file(), now);
    expect(uploader.reserve).toHaveBeenCalledTimes(1);
    pending.resolve(reserved);
    await save;
    uploader.reserve.mockRejectedValueOnce(new Error('transport broke'));
    await actions.takePhoto('lime', file(), now);
    expect(actions.photos().find((j) => j.plantId === 'lime')?.state).toBe('failed');
  });
  it('does not require secure-context-only UUID or digest APIs for LAN phone entry', async () => {
    const spy = vi.spyOn(crypto, 'randomUUID').mockImplementation(() => {
      throw new Error('secure context unavailable');
    });
    await actions.markWatered('fig', now);
    await actions.takePhoto('fig', file(), now);
    expect(spy).not.toHaveBeenCalled();
    expect(actions.photos()[0].state).toBe('saved');
  });
  it('publishes immutable job snapshots so computed selectors observe a completed save', async () => {
    const pending = deferred<any>();
    uploader.put.mockReturnValueOnce(pending.promise);
    const save = actions.takePhoto('fig', file(), now);
    await settle();
    const before = actions.photos()[0];
    expect(before.state).toBe('saving');
    pending.resolve({ ok: true, sha256: 'a'.repeat(64) });
    await save;
    const after = actions.photos()[0];
    expect(after).not.toBe(before);
    expect(before.state).toBe('saving');
    expect(after).toMatchObject({ state: 'saved', file: null, previewUrl: null });
  });
});
