import { Injectable, DestroyRef, inject, signal, effect, untracked } from '@angular/core';
import {
  AttachPhotoDocument,
  RecordActivityDocument,
  type AttachPhotoInput,
  type PhotoDateSource,
  type AttachPhotoMutation,
  type RecordActivityInput,
  type RecordActivityMutation,
} from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { browserTimezone, localDateInput } from '../journal/journal-view';
import { Uploader, type MediaProblem } from '../capture/upload';

/** Where a job stands. 'saving' covers every in-flight stage; there is no
 * optimistic 'saved' — the state only flips once the server confirms. */
export type JobState = 'saving' | 'failed' | 'saved';

/** Stages of the photo pipeline, so a retry resumes exactly where it stopped. */
export type PhotoPhase = 'reserve' | 'put' | 'finalize' | 'process' | 'attach';

/** One watering intent. `dayKey`/`occurredAt` are frozen at the tap, so a
 * retry after midnight still records the day the owner meant. */
export interface WaterJob {
  id: string;
  plantId: string;
  /** yyyy-mm-dd in the browser's local calendar, from localDateInput. */
  dayKey: string;
  occurredAt: string;
  timezone: string;
  state: JobState;
  error: string;
  activityId: string | null;
}

/** One "photo of this plant today" intent. */
export interface PhotoJob {
  id: string;
  plantId: string;
  dayKey: string;
  takenAt: string;
  timezone: string;
  /** Resolved by the server from the original photo; pending uploads use fallback. */
  dateSource: PhotoDateSource;
  state: JobState;
  error: string;
  phase: PhotoPhase;
  /** 0..100 of the byte-side PUT. */
  progress: number;
  assetId: string | null;
  photoId: string | null;
  previewUrl: string | null;
  /** Held only until the photo is saved; released afterwards. */
  file: File | null;
}

/** Internal records: the job the UI sees plus the cached intent it replays. */
interface JobCore {
  /** Workspace the intent belongs to; stale callbacks must not cross it. */
  readonly workspace: string;
  /** Session generation the intent was created in. A workspace switch (even
   * A → B → A) bumps this, so a callback from the old session can never
   * continue into the new one. */
  readonly epoch: number;
  /** Watering jobs carry their frozen mutation body; photo jobs do not. */
  readonly input?: RecordActivityInput;
  readonly clientMutationId?: string;
}

interface PhotoCore {
  /** Held until the photo is saved; nulled on save and on discard. */
  file: File | null;
  readonly takenAt: string;
  readonly timezone: string;
  readonly dateSource: PhotoDateSource;
  /** Captured at take time: the first photo of a coverless plant becomes the
   * cover; later photos can improve the cover through the server’s quality policy. */
  readonly isCover: boolean;
  reserveKey: string;
  assetId: string | null;
  /** Server-computed checksum from the PUT receipt; never a local digest. */
  serverSha: string;
  finalizeKey: string;
  /** Built once, when the asset id is known, then frozen: every retry of the
   * attach stage sends byte-for-byte the same request. */
  attachInput: AttachPhotoInput | null;
  attachKey: string;
}

interface Slot {
  job: WaterJob | PhotoJob;
  core: JobCore;
  photo?: PhotoCore;
}

const WATER_PREFIX = 'wtr';
const PHOTO_PREFIX = 'pho';

/** 128 bits of entropy as hex. crypto.getRandomValues works over plain-http
 * LAN addresses; crypto.randomUUID/subtle do not, so they are not used. */
function randomHex(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

function newId(prefix: string): string {
  return `${prefix}_${randomHex()}`;
}

function firstProblem(problems: MediaProblem[] | undefined): string | null {
  const p = problems?.[0];
  return p ? p.message : null;
}

/** One mutation outcome → a truthful sentence, including "we don't know". */
function mutationFailure(res: GqlResult<object>): string {
  if (res.kind === 'auth') return 'Session expired — sign in again, then retry.';
  if (res.kind === 'aborted') return 'The request was cancelled before it finished.';
  return 'The save did not complete. The server may or may not have recorded it — retry safely, it replays the same request.';
}

/**
 * Action coordinator for the mobile rounds page (UI15).
 *
 * Provided at root on purpose: the rounds page unmounts when the user moves to
 * the next plant or another section, and an in-flight upload must survive that.
 * Jobs therefore live here, not in the page, and the beforeunload guard is
 * owned here too so a closing tab warns while anything is unfinished.
 *
 * Every public method is one owner action → one job. Repeated taps return the
 * job that already exists instead of firing a second mutation, and a failed
 * job keeps its original input plus its clientMutationId, so retrying is the
 * same request rather than a new one.
 */
@Injectable({ providedIn: 'root' })
export class RoundActions {
  private readonly gql = inject(GraphQLClient);
  private readonly uploader = inject(Uploader);
  private readonly session = inject(SessionService);
  private readonly destroyRef = inject(DestroyRef);

  readonly waters = signal<WaterJob[]>([]);
  readonly photos = signal<PhotoJob[]>([]);

  /** Workspace the current job lists belong to. */
  private generation = this.workspaceId();
  /** Bumped on every reset; a job's captured epoch must still match it. */
  private epoch = 0;
  private readonly running = new Map<string, Slot>();
  /** Ids whose pipeline is currently in flight; a second call is a no-op. */
  private readonly busy = new Set<string>();

  constructor() {
    // Root providers outlive every route, so this listener is registered once
    // and removed when the provider itself is torn down.
    addEventListener('beforeunload', this.warnOnLeave);
    // The session lifecycle is owned here, not by the host page: whatever
    // changes the workspace or ends the session drops the old jobs, revokes
    // their previews and invalidates their in-flight callbacks. The watch
    // resets itself first so its own signal writes cannot feed back into it.
    effect(() => {
      const workspace = this.workspaceId();
      if (workspace !== this.generation) untracked(() => this.reset());
    });
    this.destroyRef.onDestroy(() => {
      removeEventListener('beforeunload', this.warnOnLeave);
      this.reset();
    });
  }

  /**
   * Kept for host pages that already call it. The service watches the session
   * itself, so a page is not required to call this for correctness; calling it
   * is simply a no-op when nothing changed.
   */
  syncWorkspace(): boolean {
    const workspace = this.workspaceId();
    if (workspace === this.generation) return false;
    this.reset();
    return true;
  }

  /**
   * True while any job still needs the browser: drives the unload warning.
   * A 'failed' job counts too — its request may already have reached the
   * server and its file/preview are still held, so reloading loses both.
   */
  hasUnfinished(): boolean {
    return (
      this.waters().some((j) => j.state !== 'saved') ||
      this.photos().some((j) => j.state !== 'saved')
    );
  }

  /**
   * Watering intent for `plantId`. The instant and the local day are captured
   * once here; nothing about them changes on retry. No quantity, unit, product
   * or note is invented — optional unknown values remain null.
   */
  async markWatered(plantId: string, now: Date = new Date()): Promise<void> {
    this.syncWorkspace();
    const workspace = this.workspaceId();
    if (!workspace) return;

    const dayKey = localDateInput(now.toISOString());
    const existing = this.waters().find((j) => j.plantId === plantId && j.dayKey === dayKey);
    if (existing) {
      // Same plant, same local day: a second tap is not a second watering.
      if (existing.state !== 'saved') await this.runWater(existing.id);
      return;
    }

    const occurredAt = now.toISOString();
    const timezone = browserTimezone();
    const job: WaterJob = {
      id: newId(WATER_PREFIX),
      plantId,
      dayKey,
      occurredAt,
      timezone,
      state: 'saving',
      error: '',
      activityId: null,
    };
    const core: JobCore = {
      workspace,
      epoch: this.epoch,
      input: Object.freeze({
        plantId,
        kind: 'WATER',
        occurredAt,
        timezone,
        quantity: null,
        unit: null,
        product: null,
        note: null,
      }),
      clientMutationId: `${job.id}:water`,
    };
    this.running.set(job.id, { job, core });
    this.waters.update((l) => [...l, { ...job }]);
    await this.runWater(job.id);
  }

  /** Replays the original watering request with its original key. */
  async retryWater(id: string): Promise<void> {
    await this.runWater(id);
  }

  /**
   * Photo intent for `plantId`. One unfinished photo per plant: while a job is
   * saving or failed the caller must discard it before a new photo, which keeps
   * a phone from stacking duplicate captures of the same plant.
   */
  async takePhoto(plantId: string, file: File, now: Date = new Date()): Promise<void> {
    this.syncWorkspace();
    const workspace = this.workspaceId();
    if (!workspace) return;

    if (this.photos().some((j) => j.plantId === plantId && j.state !== 'saved')) return;

    const takenAt = now.toISOString();
    const timezone = browserTimezone();
    const job: PhotoJob = {
      id: newId(PHOTO_PREFIX),
      plantId,
      dayKey: localDateInput(takenAt),
      takenAt,
      timezone,
      dateSource: 'UPLOAD_FALLBACK',
      state: 'saving',
      error: '',
      phase: 'reserve',
      progress: 0,
      assetId: null,
      photoId: null,
      previewUrl: this.createObjectUrl(file),
      file,
    };
    const core: PhotoCore = {
      file,
      takenAt,
      timezone,
      dateSource: 'UPLOAD_FALLBACK',
      isCover: false,
      reserveKey: `${job.id}:reserve`,
      assetId: null,
      serverSha: '',
      finalizeKey: '',
      attachInput: null,
      attachKey: '',
    };
    this.running.set(job.id, { job, core: { workspace, epoch: this.epoch }, photo: core });
    this.photos.update((l) => [...l, { ...job }]);
    await this.runPhoto(job.id);
  }

  /** Resumes the photo pipeline at the phase it stopped in — never from zero. */
  async retryPhoto(id: string): Promise<void> {
    await this.runPhoto(id);
  }

  /**
   * Drops a failed photo: revokes its preview and releases the file. Only a
   * 'failed' job can be discarded, and nothing is deleted server-side — the
   * asset and any photo row stay exactly as the server left them.
   */
  discardPhoto(id: string): void {
    const slot = this.find(id);
    if (!slot || !slot.photo || slot.job.state !== 'failed') return;
    this.releasePreview(slot.job as PhotoJob);
    slot.photo.file = null;
    (slot.job as PhotoJob).file = null;
    this.running.delete(id);
    this.photos.update((l) => l.filter((j) => j.id !== id));
  }

  // ── pipeline ──────────────────────────────────────────────────────────────

  private async runWater(id: string): Promise<void> {
    const slot = this.find(id);
    if (!slot || slot.photo) return;
    const job = slot.job as WaterJob;
    const core = slot.core;
    if (!this.fresh(slot.core.workspace, slot.core.epoch)) return this.dropStale(job);
    if (job.state === 'saved' || this.busy.has(id)) return;
    this.busy.add(id);

    this.patch(job, { state: 'saving', error: '' });
    let res: GqlResult<RecordActivityMutation>;
    try {
      res = await this.gql.mutate(RecordActivityDocument, {
        input: core.input!,
        clientMutationId: core.clientMutationId,
      });
    } catch {
      res = { kind: 'network' };
    } finally {
      this.busy.delete(id);
    }

    if (!this.fresh(core.workspace, core.epoch)) return this.dropStale(job);
    if (res.kind !== 'ok') return this.fail(job, mutationFailure(res));

    const payload = res.data?.recordActivity;
    const problem = firstProblem(payload?.problems);
    const activityId = payload?.activity?.id ?? null;
    if (res.errors.length > 0 || problem || !activityId) {
      return this.fail(job, problem ?? 'Could not confirm watering. Retry safely.');
    }
    this.patch(job, { state: 'saved', error: '', activityId });
    this.publish(this.waters);
  }

  private async runPhoto(id: string): Promise<void> {
    const slot = this.find(id);
    if (!slot?.photo) return;
    const job = slot.job as PhotoJob;
    const core = slot.photo;
    const workspace = slot.core.workspace;
    if (!this.fresh(slot.core.workspace, slot.core.epoch)) return this.dropStale(job);
    if (job.state === 'saved' || this.busy.has(id)) return;
    this.busy.add(id);

    try {
      await this.advancePhoto(job, core, workspace, slot.core.epoch);
    } catch {
      if (this.fresh(workspace, slot.core.epoch))
        this.fail(job, 'The upload stopped unexpectedly. Retry resumes where it left off.');
    } finally {
      this.busy.delete(id);
    }
  }

  /** Sequential stages; each one resumes from job.phase with cached keys. */
  private async advancePhoto(
    job: PhotoJob,
    core: PhotoCore,
    workspace: string,
    epoch: number,
  ): Promise<void> {
    if (job.phase === 'reserve') {
      this.patch(job, { state: 'saving', error: '', phase: 'reserve' });
      const file = core.file;
      if (!file)
        return this.fail(
          job,
          'The photo file is no longer available. Discard this job and take the photo again.',
        );
      const res = await this.uploader.reserve('PHOTO', file.name, file.size, core.reserveKey);
      if (!this.fresh(workspace, epoch)) return this.dropStale(job);
      if (!res.ok) return this.fail(job, res.message);
      core.assetId = res.asset.id;
      this.patch(job, { assetId: res.asset.id, phase: 'put' });
    }

    if (job.phase === 'put') {
      const assetId = core.assetId ?? job.assetId;
      const file = core.file;
      if (!assetId || !file)
        return this.fail(
          job,
          'The upload cannot continue without its file. Discard this job and take the photo again.',
        );
      this.patch(job, { state: 'saving', error: '' });
      const put = await this.uploader.put(assetId, file, (pct) => {
        if (this.fresh(workspace, epoch)) this.patch(job, { progress: pct });
      });
      if (!this.fresh(workspace, epoch)) return this.dropStale(job);
      if (!put.ok) return this.fail(job, put.message);
      // The server computes the checksum; the client never digests the file.
      if (!/^[0-9a-f]{64}$/.test(put.sha256)) {
        return this.fail(
          job,
          'The upload receipt carried no usable checksum. Retry restarts the byte transfer.',
        );
      }
      core.serverSha = put.sha256;
      core.finalizeKey = core.finalizeKey || `${job.id}:finalize`;
      this.patch(job, { phase: 'finalize' });
    }

    if (job.phase === 'finalize') {
      const assetId = core.assetId ?? job.assetId;
      if (!assetId || !core.serverSha)
        return this.fail(
          job,
          'The upload is missing its checksum. Retry restarts the byte transfer.',
        );
      this.patch(job, { state: 'saving', error: '' });
      const fin = await this.uploader.finalize(assetId, core.serverSha, core.finalizeKey);
      if (!this.fresh(workspace, epoch)) return this.dropStale(job);
      if (!fin.ok) return this.fail(job, fin.message);
      // Already READY means the server finished while we were catching up.
      this.patch(job, { phase: fin.asset.state === 'READY' ? 'attach' : 'process' });
    }

    if (job.phase === 'process') {
      const assetId = core.assetId ?? job.assetId;
      if (!assetId) return this.fail(job, 'The upload lost its reservation. Take the photo again.');
      this.patch(job, { state: 'saving', error: '' });
      const terminal = await this.uploader.waitTerminal(assetId);
      if (!this.fresh(workspace, epoch)) return this.dropStale(job);
      if (terminal.outcome === 'failed') {
        return this.fail(job, `The server could not process this image (${terminal.errorCode}).`);
      }
      if (terminal.outcome === 'timeout') {
        return this.fail(job, 'Processing is taking unusually long — retry checks the same asset.');
      }
      // 'ready' or 'gone': attach validates READY versus expired and says which.
      this.patch(job, { phase: 'attach' });
    }

    if (job.phase === 'attach') {
      const assetId = core.assetId ?? job.assetId;
      if (!assetId) return this.fail(job, 'The upload lost its reservation. Take the photo again.');
      core.attachInput =
        core.attachInput ??
        Object.freeze({
          plantId: job.plantId,
          assetId,
          takenAt: null,
          preferCaptureDate: true,
          timezone: core.timezone,
          dateSource: 'UPLOAD_FALLBACK',
          viewpoint: 'WHOLE',
          isCover: core.isCover,
          caption: null,
        });
      core.attachKey = core.attachKey || `${job.id}:attach`;
      this.patch(job, { state: 'saving', error: '' });
      let res: GqlResult<AttachPhotoMutation>;
      try {
        res = await this.gql.mutate(AttachPhotoDocument, {
          input: core.attachInput,
          clientMutationId: core.attachKey,
        });
      } catch {
        res = { kind: 'network' };
      }
      if (!this.fresh(workspace, epoch)) return this.dropStale(job);
      if (res.kind !== 'ok') {
        return this.fail(
          job,
          `${mutationFailure(res)} The asset stays as it is; retry attaches without re-uploading.`,
        );
      }
      const payload = res.data?.attachPhoto;
      const problem = firstProblem(payload?.problems);
      const photoId = payload?.photo?.id ?? null;
      if (res.errors.length > 0 || problem || !photoId) {
        return this.fail(job, problem ?? 'The server refused to attach this photo.');
      }
      const photo = payload!.photo!;
      this.patch(job, {
        state: 'saved',
        error: '',
        photoId,
        takenAt: photo.takenAt ?? job.takenAt,
        dayKey: localDateInput(photo.takenAt ?? job.takenAt),
        dateSource: photo.dateSource,
      });
      this.releasePreview(job);
      core.file = null;
      (job as PhotoJob).file = null;
      this.publish(this.photos);
    }
  }

  // ── small shared helpers ──────────────────────────────────────────────────

  private find(id: string): Slot | undefined {
    return this.running.get(id);
  }

  /** Keep private retry records mutable, but publish fresh snapshots for signal selectors. */
  private patch(job: WaterJob | PhotoJob, changes: Partial<WaterJob & PhotoJob>): void {
    Object.assign(job, changes);
    this.publish('phase' in job ? this.photos : this.waters);
  }

  private publish(ref: typeof this.waters | typeof this.photos): void {
    if (ref === this.photos)
      this.photos.update((items) =>
        items.map((item) => {
          const current = this.running.get(item.id)?.job;
          return current && 'phase' in current ? { ...current } : item;
        }),
      );
    else
      this.waters.update((items) =>
        items.map((item) => {
          const current = this.running.get(item.id)?.job;
          return current && !('phase' in current) ? { ...current } : item;
        }),
      );
  }

  private fail(job: WaterJob | PhotoJob, message: string): void {
    this.patch(job, { state: 'failed', error: message });
  }

  /** A workspace switch (or sign-out) makes every in-flight result stale. */
  private fresh(workspace: string, epoch: number): boolean {
    return (
      epoch === this.epoch &&
      this.session.state() === 'authed' &&
      workspace !== '' &&
      workspace === this.session.info()?.workspaceId &&
      workspace === this.generation
    );
  }

  private dropStale(job: WaterJob | PhotoJob): void {
    if ('phase' in job) {
      this.releasePreview(job);
      job.file = null;
    }
    this.running.delete(job.id);
    if ('phase' in job) this.photos.update((l) => l.filter((j) => j.id !== job.id));
    else this.waters.update((l) => l.filter((j) => j.id !== job.id));
  }

  private workspaceId(): string {
    return this.session.state() === 'authed' ? (this.session.info()?.workspaceId ?? '') : '';
  }

  private reset(): void {
    this.epoch++;
    this.generation = this.workspaceId();
    this.busy.clear();
    for (const slot of this.running.values()) {
      if (slot.photo && 'phase' in slot.job) {
        this.releasePreview(slot.job);
        slot.photo.file = null;
        slot.job.file = null;
      }
    }
    this.running.clear();
    this.waters.set([]);
    this.photos.set([]);
  }

  private createObjectUrl(file: File): string | null {
    // Test environments without URL.createObjectURL simply have no preview.
    if (typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') return null;
    try {
      return URL.createObjectURL(file);
    } catch {
      return null;
    }
  }

  private releasePreview(job: PhotoJob): void {
    if (!job.previewUrl) return;
    if (typeof URL !== 'undefined' && typeof URL.revokeObjectURL === 'function') {
      URL.revokeObjectURL(job.previewUrl);
    }
    job.previewUrl = null;
  }

  private readonly warnOnLeave = (event: BeforeUnloadEvent): void => {
    if (!this.hasUnfinished()) return;
    // Native confirmation only: no custom dialog, no invented wording.
    event.preventDefault();
    event.returnValue = '';
  };
}
