import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  inject,
  input,
  output,
  signal,
} from '@angular/core';
import {
  AttachPhotoDocument,
  PlantsDocument,
  type AttachPhotoMutation,
  type PhotoDateSource,
  type PhotoViewpoint,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { UiStatus } from '../../ui/status';
import { UiToasts } from '../../ui/toast';
import { browserTimezone, localInstant } from '../journal/journal-view';
import { Uploader, type MediaProblem } from './upload';

/** One row of the queue. Mutable on purpose: the pipeline patches fields as
 * it advances and calls touch() so OnPush re-renders; identity stays stable
 * for @for tracking. */
interface QueueItem {
  localId: string;
  name: string;
  size: number;
  previewUrl: string | null;
  file: File | null;
  assetId: string | null;
  state:
    | 'queued'
    | 'uploading'
    | 'processing'
    | 'ready'
    | 'attached'
    | 'failed'
    | 'needs-file'
    | 'needs-finalize';
  pct: number;
  error: string;
  viewpoint: PhotoViewpoint;
  date: string; // yyyy-mm-dd, local; blank = no capture date claimed
  time: string; // HH:MM local, optional
  caption: string;
  isCover: boolean;
  plantId: string; // per-item pick when the queue is workspace-scoped
  claimedBytes: number; // recovery: what the reservation promised
  serverSha: string; // recovery: server-computed checksum of a fully received upload
  retried: boolean; // checksum mismatch already re-reserved once
}

const VIEWPOINTS: PhotoViewpoint[] = ['WHOLE', 'LEAF', 'FRUIT', 'OTHER'];

const ERROR_LABELS: Record<string, string> = {
  UNSUPPORTED_MEDIA_TYPE: 'The file content is not an allowed image type.',
  PROCESSING_FAILED: 'The server could not process this image.',
  QUOTA_EXCEEDED: 'Workspace storage quota is full.',
};

let uid = 0;

/**
 * Resilient photo capture queue (T32.a). Per-file reserve → PUT (real
 * progress) → finalize → server-side processing → attach, with retry that
 * never loses successful siblings, reload recovery from the server's pending
 * set (no bytes or paths in browser storage), and honest date provenance:
 * an owner-stated date travels as USER with the browser zone; no date is
 * labelled UPLOAD_FALLBACK, never a disguised "now".
 */
@Component({
  selector: 'photo-queue',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [UiStatus],
  template: `
    <div class="row wrap" style="margin-top:var(--sp-3)">
      <label class="btn btn-primary" [for]="pickId" style="cursor:pointer">Choose photos</label>
      <input
        [id]="pickId"
        type="file"
        accept="image/*"
        multiple
        class="visually-only"
        (change)="pick($event)"
      />
      @if (plantId() === null) {
        <span class="caption">Pick the plant for each photo below.</span>
      }
    </div>

    @if (items().length) {
      <ul
        class="stack"
        style="list-style:none; margin:var(--sp-4) 0 0; padding:0"
        aria-label="Upload queue"
      >
        @for (f of items(); track f.localId) {
          <li class="card card-raised">
            <div class="row spread">
              <strong class="small mono">{{ f.name }}</strong>
              @switch (f.state) {
                @case ('attached') {
                  <ui-status kind="ok" label="Attached" />
                }
                @case ('processing') {
                  <ui-status kind="warn" label="Processing" />
                }
                @case ('needs-finalize') {
                  <ui-status kind="warn" label="Finalizing" />
                }
                @case ('uploading') {
                  <ui-status kind="warn" label="Uploading {{ f.pct }}%" />
                }
                @case ('failed') {
                  <ui-status kind="danger" label="Failed" />
                }
                @case ('needs-file') {
                  <ui-status kind="danger" label="Needs file" />
                }
                @case ('ready') {
                  <ui-status kind="ok" label="Ready" />
                }
                @default {
                  <span class="badge">Queued</span>
                }
              }
            </div>

            @if (f.previewUrl) {
              <img
                [src]="f.previewUrl"
                [alt]="'Preview of ' + f.name"
                style="max-height:96px; max-width:100%; border-radius:var(--radius-card); margin-top:var(--sp-2)"
              />
            }
            @if (f.state === 'uploading') {
              <div
                role="progressbar"
                [attr.aria-valuenow]="f.pct"
                aria-valuemin="0"
                aria-valuemax="100"
                [attr.aria-label]="'Upload progress for ' + f.name"
                style="height:8px; border-radius:99px; background:var(--surface-canvas); border:1px solid var(--border); margin:var(--sp-2) 0"
              >
                <div
                  [style.width.%]="f.pct"
                  style="height:100%; background:var(--accent); border-radius:99px"
                ></div>
              </div>
            }
            @if (f.error) {
              <p class="field-error" style="margin-top:var(--sp-2)">
                {{ f.error }}
              </p>
            }

            @if (f.state !== 'attached') {
              <div class="row wrap" style="margin-top:var(--sp-2)">
                @if (plantId() === null) {
                  <label class="caption" [for]="f.localId + '-plant'">Plant</label>
                  <select
                    class="input"
                    [id]="f.localId + '-plant'"
                    style="max-width:180px"
                    [value]="f.plantId"
                    (change)="pickPlant(f, $any($event.target).value)"
                  >
                    <option value="">choose…</option>
                    @for (p of plants(); track p.id) {
                      <option [value]="p.id">{{ p.name }}</option>
                    }
                  </select>
                }
                <label class="caption" [for]="f.localId + '-vp'">Viewpoint</label>
                <select
                  class="input"
                  [id]="f.localId + '-vp'"
                  style="max-width:120px"
                  [value]="f.viewpoint"
                  (change)="f.viewpoint = $any($event.target).value; touch()"
                >
                  @for (v of viewpoints; track v) {
                    <option [value]="v">{{ v.toLowerCase() }}</option>
                  }
                </select>
                <label class="caption" [for]="f.localId + '-date'">Taken on</label>
                <input
                  class="input"
                  type="date"
                  [id]="f.localId + '-date'"
                  style="max-width:150px"
                  [value]="f.date"
                  (change)="f.date = $any($event.target).value; touch()"
                />
                <label class="caption" [for]="f.localId + '-time'">Time</label>
                <input
                  class="input"
                  type="time"
                  [id]="f.localId + '-time'"
                  style="max-width:110px"
                  [value]="f.time"
                  (change)="f.time = $any($event.target).value; touch()"
                />
                <label class="chip chip-check">
                  <input
                    type="checkbox"
                    [checked]="f.isCover"
                    (change)="f.isCover = $any($event.target).checked; touch()"
                  />
                  {{ hasCover() ? 'Choose this cover instead' : 'Choose as cover' }}
                </label>
              </div>
              <p class="caption" style="margin:var(--sp-1) 0 0">
                {{
                  f.date
                    ? 'Your stated date travels with your timezone.'
                    : 'Reads the capture date from your original photo. Without a camera offset, uses your device timezone. If missing, upload date is labelled separately.'
                }}
              </p>
            }

            <div class="row" style="margin-top:var(--sp-2)">
              @if (f.state === 'failed' && f.file) {
                <button type="button" class="btn" (click)="retry(f)">Retry</button>
              }
              @if (f.state === 'needs-file') {
                <label class="btn" [for]="f.localId + '-re'" style="cursor:pointer"
                  >Reselect file</label
                >
                <input
                  [id]="f.localId + '-re'"
                  type="file"
                  accept="image/*"
                  class="visually-only"
                  (change)="reselect(f, $event)"
                />
              }
              <button type="button" class="btn btn-danger" (click)="remove(f)">Remove</button>
            </div>
          </li>
        }
      </ul>
      <p class="caption" style="margin-top:var(--sp-4)">
        Pending uploads survive a reload for 24 h; fully received ones finish without reselection,
        incomplete ones restart from byte zero after you reselect the file.
      </p>
    }
  `,
  styles: [
    `
      .row.wrap {
        flex-wrap: wrap;
      }
      .chip-check {
        display: inline-flex;
        gap: var(--sp-2);
        align-items: center;
        cursor: pointer;
      }
      .chip-check input {
        accent-color: var(--accent);
      }
    `,
  ],
})
export class PhotoQueue implements OnInit, OnDestroy {
  /** Fixed plant when embedded in plant detail; null = workspace scope with a picker. */
  readonly plantId = input<string | null>(null);
  /** Presentation hint only. Automatic cover selection belongs to the server;
   * checking the per-photo choice explicitly overrides its quality policy. */
  readonly hasCover = input<boolean>(false);
  /** A photo was attached — host reloads its photo list / timeline. */
  readonly changed = output<void>();

  protected readonly uploader = inject(Uploader);
  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  protected readonly pickId = `pq-pick-${++uid}`;
  protected readonly viewpoints = VIEWPOINTS;

  protected readonly items = signal<QueueItem[]>([]);
  protected readonly plants = signal<{ id: string; name: string }[]>([]);

  /** Attach calls actually awaiting the GraphQL mutation (the item may still
   * read 'ready' while the request is in flight). */
  private readonly attaching = signal(0);

  /** Host dialogs use these as signals: true while the pipeline is working.
   * 'ready'/'failed'/'needs-file' rows are idle — they don't lock a dialog. */
  readonly busy = computed(
    () =>
      this.attaching() > 0 ||
      this.items().some(
        (i) =>
          i.state === 'queued' ||
          i.state === 'uploading' ||
          i.state === 'processing' ||
          i.state === 'needs-finalize',
      ),
  );

  /** True when any row has not reached 'attached' yet (empty queue = false). */
  readonly dirty = computed(() => this.items().some((i) => i.state !== 'attached'));

  async ngOnInit(): Promise<void> {
    if (this.plantId() === null) {
      const res = await this.gql.query(PlantsDocument, { first: 100 });
      if (res.kind === 'ok' && res.data) {
        this.plants.set(res.data.plants.items.map((p) => ({ id: p.id, name: p.name })));
      }
    }
    await this.recover();
  }

  ngOnDestroy(): void {
    for (const it of this.items()) if (it.previewUrl) URL.revokeObjectURL(it.previewUrl);
  }

  protected touch(): void {
    this.items.update((l) => [...l]);
  }

  protected pick(ev: Event): void {
    const input = ev.target as HTMLInputElement;
    for (const file of Array.from(input.files ?? [])) this.enqueue(file);
    input.value = ''; // allow reselecting the same file later
  }

  private enqueue(file: File): void {
    const item: QueueItem = {
      localId: `q${++uid}`,
      name: file.name,
      size: file.size,
      previewUrl: URL.createObjectURL(file),
      file,
      assetId: null,
      state: 'queued',
      pct: 0,
      error: '',
      viewpoint: 'WHOLE',
      date: '',
      time: '',
      caption: '',
      isCover: false,
      plantId: this.plantId() ?? '',
      claimedBytes: file.size,
      serverSha: '',
      retried: false,
    };
    this.items.update((l) => [...l, item]);
    void this.run(item);
  }

  /** Full client-side pipeline for one file. */
  private async run(item: QueueItem): Promise<void> {
    if (!item.file) return;
    try {
      const res = await this.uploader.reserve('PHOTO', item.name, item.file.size);
      if (!res.ok) return this.fail(item, res.message);
      item.assetId = res.asset.id;
      item.state = 'uploading';
      this.touch();
      const put = await this.uploader.put(res.asset.id, item.file, (pct) => {
        item.pct = pct;
        this.touch();
      });
      if (!put.ok) return this.fail(item, put.message);
      const fin = await this.uploader.finalize(res.asset.id, put.sha256);
      if (!fin.ok) {
        // A checksum mismatch is a fresh start, not a dead end: re-reserve once.
        if (fin.code === 'VALIDATION' && !item.retried) {
          item.retried = true;
          item.error = 'Checksum mismatch — created a fresh reservation and restarted the upload.';
          this.touch();
          return this.run(item);
        }
        return this.fail(item, fin.message);
      }
      await this.processAndAttach(item);
    } catch (err) {
      this.fail(item, `Upload error: ${String(err)}`);
    }
  }

  /** Wait for the server processor, then attach with the item's current metadata. */
  private async processAndAttach(item: QueueItem): Promise<void> {
    if (!item.assetId) return;
    item.state = 'processing';
    item.error = '';
    this.touch();
    const t = await this.uploader.waitTerminal(item.assetId);
    if (t.outcome === 'failed') return this.fail(item, ERROR_LABELS[t.errorCode] ?? t.errorCode);
    if (t.outcome === 'timeout')
      return this.fail(item, 'Processing is taking unusually long — retry in a moment.');
    if (t.outcome === 'gone' && !item.retried) {
      // Left the pending set: either READY (attach will confirm) or expired.
    }
    await this.attach(item);
  }

  private async attach(item: QueueItem): Promise<void> {
    const plant = this.plantId() ?? item.plantId;
    if (!plant) {
      item.state = 'ready';
      item.error = 'Choose a plant to attach this photo.';
      this.touch();
      return;
    }
    if (!item.assetId) return;
    const takenAt = item.date ? localInstant(item.date, item.time) : null;
    if (item.date && !takenAt) return this.fail(item, 'That date is not valid.');
    const dateSource: PhotoDateSource = takenAt ? 'USER' : 'UPLOAD_FALLBACK';
    this.attaching.update((n) => n + 1);
    let res: GqlResult<AttachPhotoMutation>;
    try {
      res = await this.gql.mutate(AttachPhotoDocument, {
        input: {
          plantId: plant,
          assetId: item.assetId,
          viewpoint: item.viewpoint,
          dateSource,
          preferCaptureDate: !takenAt,
          takenAt,
          timezone: browserTimezone(),
          caption: item.caption || null,
          isCover: item.isCover,
        },
      });
    } finally {
      this.attaching.update((n) => n - 1);
    }
    if (res.kind === 'auth') return this.fail(item, 'Session expired — sign in again.');
    if (res.kind !== 'ok')
      return this.fail(
        item,
        'Could not reach the server; the photo is still processing server-side.',
      );
    const problems = (res.data?.attachPhoto?.problems ?? []) as MediaProblem[];
    if (problems.length > 0) return this.fail(item, problems[0].message);
    item.state = 'attached';
    item.error = '';
    this.touch();
    this.changed.emit();
  }

  /** Reload recovery: the server keeps RESERVED/UPLOADED/PROCESSING for 24 h. */
  private async recover(): Promise<void> {
    const pending = await this.uploader.pending();
    const known = new Set(this.items().map((i) => i.assetId));
    const restored: QueueItem[] = [];
    for (const a of pending) {
      if (a.purpose !== 'PHOTO' || known.has(a.id)) continue;
      const isFailed = a.state === 'FAILED';
      const item: QueueItem = {
        localId: `q${++uid}`,
        name: a.displayName ?? 'Untitled upload',
        size: a.byteCount,
        previewUrl: null, // bytes never lived in, or were lost from, browser storage
        file: null,
        assetId: a.id,
        state:
          a.state === 'RESERVED'
            ? 'needs-file'
            : a.state === 'UPLOADED'
              ? 'needs-finalize'
              : isFailed
                ? 'failed'
                : 'processing',
        pct: 100,
        error:
          a.state === 'RESERVED'
            ? 'This upload never completed. Reselect the file to restart it from byte zero.'
            : isFailed
              ? (ERROR_LABELS[a.errorCode ?? ''] ??
                a.errorCode ??
                'The server could not process this image.')
              : '',
        viewpoint: 'WHOLE',
        date: '',
        time: '',
        caption: '',
        isCover: false,
        plantId: this.plantId() ?? '',
        claimedBytes: a.byteCount,
        serverSha: a.sha256 ?? '',
        retried: false,
      };
      restored.push(item);
      if (item.state === 'needs-finalize') void this.finalizeRecovered(item);
      if (item.state === 'processing') void this.processAndAttach(item);
    }
    if (restored.length > 0) this.items.update((l) => [...l, ...restored]);
  }

  /** A fully received upload finalizes without reselection (idempotent). */
  private async finalizeRecovered(item: QueueItem): Promise<void> {
    if (!item.assetId || !item.serverSha) return;
    const fin = await this.uploader.finalize(item.assetId, item.serverSha);
    if (!fin.ok) return this.fail(item, fin.message);
    await this.processAndAttach(item);
  }

  protected pickPlant(item: QueueItem, plantId: string): void {
    item.plantId = plantId;
    this.touch();
    // A processed photo waiting on a plant pick attaches the moment one lands.
    if (item.state === 'ready') void this.attach(item);
  }

  protected retry(item: QueueItem): void {
    item.error = '';
    item.state = 'queued';
    this.touch();
    void this.run(item); // failed checksum creates a new reservation
  }

  /** Reattach a reselected file to its existing reservation — with a size
   * check first, so the wrong file never lands on the wrong reservation. */
  protected reselect(item: QueueItem, ev: Event): void {
    const input = ev.target as HTMLInputElement;
    const file = Array.from(input.files ?? [])[0];
    input.value = '';
    if (!file || !item.assetId) return;
    if (file.size !== item.claimedBytes) {
      item.error = `${file.name} did not match its reservation size. Reselect the original file to continue.`;
      this.touch();
      return;
    }
    item.file = file;
    item.previewUrl = URL.createObjectURL(file);
    void (async () => {
      item.state = 'uploading';
      this.touch();
      const put = await this.uploader.put(item.assetId!, file, (pct) => {
        item.pct = pct;
        this.touch();
      });
      if (!put.ok) return this.fail(item, put.message);
      const fin = await this.uploader.finalize(item.assetId!, put.sha256);
      if (!fin.ok) return this.fail(item, fin.message);
      await this.processAndAttach(item);
    })();
  }

  protected remove(item: QueueItem): void {
    if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
    this.items.update((l) => l.filter((x) => x !== item));
  }

  private fail(item: QueueItem, message: string): void {
    item.state = 'failed';
    item.error = message;
    this.touch();
    this.toasts.error(`${item.name}: ${message}`);
  }
}
