import { ChangeDetectionStrategy, Component, inject, signal, viewChild } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import {
  ArchivePlantDocument,
  RestorePlantDocument,
  CreateLocationDocument,
  CreatePlantDocument,
  CreateTagDocument,
  LocationsDocument,
  PlantDetailDocument,
  TagsDocument,
  UpdatePlantDocument,
  type ContextPlacement,
  type LocationSituation,
  type PlantCategory,
  type PlantType,
} from '@canopy/contracts';
import { requestId } from '../../core/request-id';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiToasts } from '../../ui/toast';
import {
  blankToNull,
  conflictNote,
  generalProblems,
  parseCount,
  parseDecimal,
  problemMap,
  type PayloadProblem,
} from './shared';

const CATEGORIES: PlantCategory[] = ['CITRUS', 'FIG', 'STRAWBERRY', 'BLACKBERRY', 'OTHER'];
const SITUATIONS: LocationSituation[] = ['INDOOR', 'COVERED', 'OUTDOOR'];
const PLACEMENTS: ContextPlacement[] = ['GROUND', 'CONTAINER'];

/**
 * Add/edit plant (T10). One component, two modes, driven by the route:
 * /plants/new creates with optional initial context + acquisition;
 * /plants/:id/edit updates identity fields under optimistic revision
 * control. Cultivar and count may stay unknown — blanks travel as null,
 * never as invented values. Pricing is deliberately absent (T11).
 *
 * The form is modal data entry (UI02): it projects into the shared
 * UiEntryDialog shell, whose header owns the title (h2) — no page h1, no
 * inline card/max-width. Every native control, id and error binding is
 * unchanged. Cancel goes through requestClose(), so an unsaved draft asks
 * Keep editing / Discard; a completed save marks the form pristine and
 * approves the entry before the success navigation, so it never prompts.
 */
@Component({
  selector: 'plant-form',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, UiEntryDialog],
  template: `
    <!-- Modal data entry: the shared shell wraps EVERY state — loading,
         not-found and the form — so #entry stays registered and the portal
         never disappears mid-navigation (reloadLatest). The shell owns the
         title (h2); every native control, id and error binding is unchanged. -->
    <ui-entry-dialog
      #entry
      [open]="true"
      [title]="mode() === 'edit' ? 'Edit plant' : 'Add a plant'"
      [busy]="saving()"
      [dirty]="form.dirty"
      (dismissed)="dismiss()"
    >
      @if (mode() === 'edit' && loadState() === 'loading') {
        <p class="caption">Loading plant…</p>
      } @else if (mode() === 'edit' && loadState() === 'missing') {
        <section class="card" role="alert" style="max-width:560px">
          <h2>Plant not found</h2>
          <p class="caption">It may have been removed, or it belongs to another workspace.</p>
          <div>
            <button type="button" class="btn btn-primary" (click)="dismiss()">
              Back to collection
            </button>
          </div>
        </section>
      } @else {
        @if (conflict()) {
          <section class="form-section" role="alert" aria-labelledby="cf-h">
            <h2 id="cf-h">This plant changed elsewhere</h2>
            <p class="caption">{{ conflict() }}</p>
            <div class="form-actions">
              <button type="button" class="btn btn-primary" (click)="reloadLatest()">
                Load the latest version
              </button>
            </div>
          </section>
        }
        @if (notices().length) {
          <section class="form-section" role="alert">
            @for (n of notices(); track n) {
              <p class="caption" style="margin:0">{{ n }}</p>
            }
          </section>
        }

        <form [formGroup]="form" (ngSubmit)="submit()" novalidate>
          <section class="form-section" aria-labelledby="id-h">
            <h2 id="id-h">Identity</h2>

            <label class="field-label" for="f-name">Name</label>
            <input
              id="f-name"
              class="input"
              formControlName="name"
              required
              [attr.aria-invalid]="err('name') ? 'true' : null"
              [attr.aria-describedby]="err('name') ? 'e-name' : null"
            />
            @if (err('name')) {
              <p class="field-error" id="e-name">{{ err('name') }}</p>
            }

            <label class="field-label" for="f-sci">Scientific name</label>
            <input
              id="f-sci"
              class="input"
              formControlName="scientificName"
              placeholder="optional"
            />

            <label class="field-label" for="f-cult">Cultivar</label>
            <input
              id="f-cult"
              class="input"
              formControlName="cultivar"
              placeholder="leave blank if unknown"
            />
            <p class="field-hint">Blank means “unknown” — it is never guessed.</p>

            <label class="field-label" for="f-root">Rootstock</label>
            <input id="f-root" class="input" formControlName="rootstock" placeholder="optional" />

            <div class="form-grid">
              <div>
                <label class="field-label" for="f-type">Type</label>
                <select id="f-type" class="input" formControlName="type">
                  <option value="INDIVIDUAL">Individual plant</option>
                  <option value="GROUP">Group / patch</option>
                </select>
              </div>
              <div>
                <label class="field-label" for="f-count">Count</label>
                <input
                  id="f-count"
                  class="input"
                  formControlName="count"
                  inputmode="numeric"
                  placeholder="blank = unknown"
                />
                @if (err('count')) {
                  <p class="field-error" id="e-count">{{ err('count') }}</p>
                }
              </div>
            </div>

            <label class="field-label" for="f-cat">Category</label>
            <select id="f-cat" class="input" formControlName="category">
              @for (c of categories; track c) {
                <option [value]="c">{{ c.toLowerCase() }}</option>
              }
            </select>

            <div class="form-grid">
              <div>
                <label class="field-label" for="f-planted">Planted on</label>
                <input id="f-planted" class="input" type="date" formControlName="plantedOn" />
              </div>
              <div>
                <label class="field-label" for="f-age">Estimated age (years)</label>
                <input
                  id="f-age"
                  class="input"
                  formControlName="estimatedAgeYears"
                  inputmode="decimal"
                  placeholder="optional"
                />
                @if (err('estimatedAgeYears')) {
                  <p class="field-error" id="e-age">
                    {{ err('estimatedAgeYears') }}
                  </p>
                }
              </div>
            </div>
          </section>

          @if (mode() === 'create') {
            <section class="form-section" aria-labelledby="ctx-h">
              <h2 id="ctx-h">Where is it now?</h2>
              <p class="field-hint">
                Optional — you can move a plant later; every move is kept as history.
              </p>

              <label class="field-label" for="f-loc">Location</label>
              <select id="f-loc" class="input" formControlName="locationId">
                <option value="">No location</option>
                @for (l of locations(); track l.id) {
                  <option [value]="l.id">
                    {{ l.name }}{{ l.parent ? ' · ' + l.parent.name : '' }}
                  </option>
                }
              </select>

              <div class="form-grid">
                <div>
                  <label class="field-label" for="f-place">Placement</label>
                  <select id="f-place" class="input" formControlName="placement">
                    @for (p of placements; track p) {
                      <option [value]="p">{{ p.toLowerCase() }}</option>
                    }
                  </select>
                </div>
                <div>
                  <label class="field-label" for="f-pot">Pot volume (L)</label>
                  <input
                    id="f-pot"
                    class="input"
                    formControlName="potVolumeLiters"
                    inputmode="decimal"
                    placeholder="blank = unknown"
                  />
                  @if (err('potVolumeLiters')) {
                    <p class="field-error" id="e-pot">
                      {{ err('potVolumeLiters') }}
                    </p>
                  }
                </div>
              </div>

              <label class="field-label" for="f-newloc">Or add a new location</label>
              <input
                id="f-newloc"
                class="input"
                formControlName="newLocation"
                placeholder="e.g. Kitchen window"
              />
              <div class="form-grid">
                <div>
                  <label class="field-label" for="f-newsit">Situation</label>
                  <select id="f-newsit" class="input" formControlName="newLocationSituation">
                    @for (s of situations; track s) {
                      <option [value]="s">{{ s.toLowerCase() }}</option>
                    }
                  </select>
                </div>
                <div>
                  <label class="field-label" for="f-newparent">Inside (optional)</label>
                  <select id="f-newparent" class="input" formControlName="newLocationParent">
                    <option value="">Top level</option>
                    @for (l of locations(); track l.id) {
                      <option [value]="l.id">{{ l.name }}</option>
                    }
                  </select>
                </div>
              </div>
              @if (err('newLocation')) {
                <p class="field-error">{{ err('newLocation') }}</p>
              }
            </section>

            <section class="form-section" aria-labelledby="acq-h">
              <h2 id="acq-h">How did you get it?</h2>
              <p class="field-hint">
                Optional provenance. Prices are recorded as unknown for now — pricing tools come
                later.
              </p>
              <div class="form-grid">
                <div>
                  <label class="field-label" for="f-seller">Seller</label>
                  <input
                    id="f-seller"
                    class="input"
                    formControlName="seller"
                    placeholder="optional"
                  />
                </div>
                <div>
                  <label class="field-label" for="f-acq">Acquired on</label>
                  <input id="f-acq" class="input" type="date" formControlName="acquiredOn" />
                </div>
              </div>
            </section>

            <section class="form-section" aria-labelledby="tag-h">
              <h2 id="tag-h">Tags</h2>
              @if (availableTags().length) {
                <div class="row wrap" role="group" aria-label="Existing tags">
                  @for (t of availableTags(); track t.id) {
                    <label class="chip chip-check">
                      <input
                        type="checkbox"
                        [checked]="selectedTags().includes(t.id)"
                        (change)="toggleTag(t.id)"
                      />
                      {{ t.label }}
                    </label>
                  }
                </div>
              }
              <div class="row">
                <label class="field-label" for="f-newtag" style="margin:0">New tag</label>
                <input
                  id="f-newtag"
                  class="input"
                  [value]=""
                  #newTagInput
                  placeholder="e.g. balcony"
                  maxlength="40"
                />
                <button type="button" class="btn" (click)="addTag(newTagInput.value)">
                  Add tag
                </button>
              </div>
              @if (err('newTag')) {
                <p class="field-error">{{ err('newTag') }}</p>
              }
            </section>
          }

          @if (mode() === 'edit') {
            <details class="plant-management">
              <summary>Plant management</summary>
              <p class="caption">
                {{
                  plantStatus() === 'ARCHIVED'
                    ? 'Restore this plant to your active collection.'
                    : 'Archive this plant to hide it from your collection. Its photos and history stay intact.'
                }}
              </p>
              @if (form.dirty) {
                <p class="caption">Save or discard your edits before changing archive status.</p>
              }
              @if (archiveConfirm()) {
                <div class="archive-confirmation">
                  <p>
                    {{
                      plantStatus() === 'ARCHIVED' ? 'Restore this plant?' : 'Archive this plant?'
                    }}
                  </p>
                  <div class="row wrap">
                    <button
                      type="button"
                      class="btn"
                      [disabled]="saving()"
                      (click)="archiveConfirm.set(false)"
                    >
                      Keep as is</button
                    ><button
                      type="button"
                      class="btn"
                      [disabled]="saving() || form.dirty"
                      (click)="changeArchiveStatus()"
                    >
                      {{ plantStatus() === 'ARCHIVED' ? 'Confirm restore' : 'Confirm archive' }}
                    </button>
                  </div>
                </div>
              } @else {
                <button
                  type="button"
                  class="btn"
                  [disabled]="saving() || form.dirty"
                  (click)="archiveConfirm.set(true)"
                >
                  {{ plantStatus() === 'ARCHIVED' ? 'Restore plant' : 'Archive plant' }}
                </button>
              }
            </details>
          }
          <div class="form-actions">
            <button type="button" class="btn" (click)="entry.requestClose()">Cancel</button>
            <button type="submit" class="btn btn-primary" [disabled]="saving()">
              {{ saving() ? 'Saving…' : mode() === 'edit' ? 'Save changes' : 'Add plant' }}
            </button>
          </div>
        </form>
      }
    </ui-entry-dialog>
  `,
  styles: [
    `
      .plant-management {
        border-top: 1px solid var(--border-decorative);
        padding-top: 20px;
        margin-top: 8px;
      }
      .plant-management summary {
        cursor: pointer;
        color: var(--text-muted);
        font-size: 13px;
        padding: 8px 0;
      }
      .plant-management .caption {
        margin: 12px 0;
      }
      .archive-confirmation {
        padding: 16px;
        border: 1px solid var(--border-decorative);
        border-radius: 12px;
      }
      .archive-confirmation p {
        margin-top: 0;
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
      .field-error {
        color: var(--danger);
        font-size: 13px;
        margin: var(--sp-1) 0 0;
      }
      .row.wrap {
        flex-wrap: wrap;
      }
      /* the shared .entry-form/.form-section/.form-grid/.form-actions classes
         live in styles/base.css and reach the projected content through the
         dialog shell; nothing here overrides their spacing */
      .form-section h2 {
        font-size: 19px;
        line-height: 28px;
        font-weight: 600;
        margin: 0;
      }
    `,
  ],
})
export class PlantForm {
  private readonly gql = inject(GraphQLClient);
  private readonly fb = inject(NonNullableFormBuilder);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly toasts = inject(UiToasts);
  private readonly session = inject(SessionService);

  protected readonly categories = CATEGORIES;
  protected readonly situations = SITUATIONS;
  protected readonly placements = PLACEMENTS;

  protected readonly mode = signal<'create' | 'edit'>('create');
  protected readonly plantId = signal('');
  protected readonly loadState = signal<'loading' | 'ready' | 'missing'>('ready');
  protected readonly saving = signal(false);
  protected readonly plantStatus = signal('ACTIVE');
  protected readonly archiveConfirm = signal(false);
  protected readonly conflict = signal('');
  protected readonly notices = signal<string[]>([]);
  protected readonly serverErr = signal<Record<string, string>>({});
  protected readonly locations = signal<
    { id: string; name: string; parent: { name: string } | null }[]
  >([]);
  protected readonly availableTags = signal<{ id: string; label: string }[]>([]);
  protected readonly selectedTags = signal<string[]>([]);

  /** The modal host: Cancel routes through requestClose() (dirty-guarded), and
   * a completed save calls markPristine() so the success navigation is never
   * blocked by the entry guard. The shell wraps every state, so it always
   * renders; optional only to keep the lookup defensive. */
  private readonly entry = viewChild<UiEntryDialog>('entry');

  /** One idempotency key per form session: a double-submit replays, never duplicates. */
  private readonly cmid = requestId();
  private expectedRevision = 0;

  protected readonly form = this.fb.group({
    name: ['', [Validators.required, Validators.maxLength(160)]],
    scientificName: [''],
    cultivar: [''],
    rootstock: [''],
    type: ['INDIVIDUAL' as PlantType],
    count: [''],
    category: ['OTHER' as PlantCategory],
    plantedOn: [''],
    estimatedAgeYears: [''],
    locationId: [''],
    placement: ['CONTAINER' as ContextPlacement],
    potVolumeLiters: [''],
    newLocation: [''],
    newLocationParent: [''],
    newLocationSituation: ['INDOOR' as LocationSituation],
    seller: [''],
    acquiredOn: [''],
  });

  constructor() {
    // The form renders at /plants/new or /plants/:id/edit. The parent route
    // owns the :id parameter, so read it explicitly instead of relying on any
    // params-inheritance setting. Direct-route unit mocks may not provide a
    // parent route at all — the fallback keeps them null-safe.
    const readId = (r: ActivatedRoute | null): string | null => {
      let node = r;
      while (node) {
        const id = node.snapshot.paramMap.get('id');
        if (id) return id;
        node = node.parent;
      }
      return null;
    };
    const id = readId(this.route);
    if (id) {
      this.mode.set('edit');
      this.plantId.set(id);
      void this.loadPlant(id);
    } else {
      this.mode.set('create');
    }
    void this.loadPickers();
  }

  /** Safe parent for Cancel/dismissal: the list behind a new plant, the
   * detail page behind an edit. */
  private parentPath(): string[] {
    return this.mode() === 'edit' ? ['/plants', this.plantId()] : ['/plants'];
  }

  /** Modal dismissed (Cancel, X, Escape, backdrop) — leave to the parent. */
  protected dismiss(): void {
    void this.router.navigate(this.parentPath());
  }

  protected err(field: string): string {
    const ctrl = this.form.controls[field as keyof typeof this.form.controls];
    if (ctrl && ctrl.invalid && ctrl.touched) {
      if (ctrl.hasError('required')) return 'This field is required.';
      if (ctrl.hasError('maxlength')) return 'Too long.';
    }
    return this.serverErr()[field] ?? '';
  }

  protected toggleTag(id: string): void {
    this.selectedTags.update((ids) =>
      ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id],
    );
  }

  /** Tag creation is immediate so the new tag can be selected right away. */
  protected async addTag(label: string): Promise<void> {
    const t = label.trim();
    if (!t) return;
    const res = await this.gql.mutate(CreateTagDocument, {
      input: { label: t },
      clientMutationId: `${this.cmid}:tag:${t}`,
    });
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    const problems = (res.kind === 'ok' ? res.data?.createTag?.problems : []) ?? [];
    if (problems.length > 0) {
      this.serverErr.set({ ...this.serverErr(), newTag: problems[0].message });
      return;
    }
    const tag = res.kind === 'ok' ? res.data?.createTag?.tag : undefined;
    if (tag) {
      this.availableTags.update((ts) =>
        ts.some((x) => x.id === tag.id) ? ts : [...ts, { id: tag.id, label: tag.label }],
      );
      this.selectedTags.update((ids) => [...ids, tag.id]);
      this.serverErr.update((e) => ({ ...e, newTag: '' }));
    }
  }

  private async loadPickers(): Promise<void> {
    const [loc, tags] = await Promise.all([
      this.gql.query(LocationsDocument),
      this.gql.query(TagsDocument, { first: 100 }),
    ]);
    if (loc.kind === 'ok' && loc.data) this.locations.set(loc.data.locations);
    if (tags.kind === 'ok' && tags.data)
      this.availableTags.set(tags.data.tags.items.filter((t) => !t.archivedAt));
  }

  private async loadPlant(id: string): Promise<void> {
    this.loadState.set('loading');
    const res = await this.gql.query(PlantDetailDocument, { id });
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind !== 'ok' || !res.data) {
      this.loadState.set('ready');
      this.notices.set(['Could not load the plant. Check your connection and retry.']);
      return;
    }
    const p = res.data.plant;
    if (!p) {
      this.loadState.set('missing');
      return;
    }
    this.loadState.set('ready');
    this.expectedRevision = p.revision;
    this.plantStatus.set(p.status);
    this.form.patchValue({
      name: p.name,
      scientificName: p.scientificName ?? '',
      cultivar: p.cultivar ?? '',
      rootstock: p.rootstock ?? '',
      type: p.type,
      count: p.count == null ? '' : String(p.count),
      category: p.category,
      plantedOn: p.plantedOn ?? '',
      estimatedAgeYears: p.estimatedAgeYears ?? '',
    });
  }

  protected reloadLatest(): void {
    this.conflict.set('');
    void this.loadPlant(this.plantId()).then(() => {
      // A successful read re-syncs the form with the server, so the draft is
      // no longer at risk — the modal stops prompting on the next dismissal.
      if (this.loadState() === 'ready') {
        this.form.markAsPristine();
        this.entry()?.markPristine();
      }
    });
  }

  protected async submit(): Promise<void> {
    if (this.saving()) return;
    this.form.markAllAsTouched();
    this.serverErr.set({});
    this.notices.set([]);
    this.conflict.set('');

    const v = this.form.getRawValue();
    const local: Record<string, string> = {};
    if (this.form.invalid) {
      if (this.form.controls.name.invalid) local['name'] = 'A name is required.';
    }
    const count = parseCount(v.count);
    if (count.error) local['count'] = count.error;
    const age = parseDecimal(v.estimatedAgeYears);
    if (age.error) local['estimatedAgeYears'] = age.error;
    const pot = parseDecimal(v.potVolumeLiters);
    if (pot.error) local['potVolumeLiters'] = pot.error;
    if (Object.keys(local).length > 0) {
      this.serverErr.set(local);
      return;
    }

    this.saving.set(true);
    try {
      if (this.mode() === 'edit') {
        await this.submitEdit(v, count, age);
      } else {
        await this.submitCreate(v, count, age, pot);
      }
    } finally {
      this.saving.set(false);
    }
  }

  protected async changeArchiveStatus(): Promise<void> {
    if (this.saving() || this.form.dirty || !this.archiveConfirm() || this.mode() !== 'edit')
      return;
    this.saving.set(true);
    this.notices.set([]);
    const archived = this.plantStatus() === 'ARCHIVED';
    try {
      const variables = {
        input: { id: this.plantId(), expectedRevision: this.expectedRevision },
        clientMutationId: `${this.cmid}:${archived ? 'restore' : 'archive'}`,
      };
      const result = archived
        ? await this.gql.mutate(RestorePlantDocument, variables)
        : await this.gql.mutate(ArchivePlantDocument, variables);
      if (result.kind === 'auth') {
        this.session.markExpired();
        return;
      }
      if (result.kind !== 'ok' || !result.data) {
        this.notices.set([
          'Could not reach the server. Archive status was not confirmed. Retry with this form.',
        ]);
        return;
      }
      const payload =
        'restorePlant' in result.data ? result.data.restorePlant : result.data.archivePlant;
      if (!payload || payload.problems.length) {
        const problems = (payload?.problems ?? []) as PayloadProblem[];
        const conflict = conflictNote(problems, 'This plant');
        if (conflict) this.conflict.set(conflict);
        this.notices.set(
          problems.length ? problems.map((p) => p.message) : ['Plant status could not be updated.'],
        );
        return;
      }
      this.form.markAsPristine();
      this.entry()?.markPristine();
      this.toasts.ok(archived ? 'Plant restored' : 'Plant archived');
      await this.router.navigate(this.parentPath());
    } finally {
      this.saving.set(false);
    }
  }

  private async submitEdit(
    v: ReturnType<typeof this.form.getRawValue>,
    count: { value: number | null },
    age: { value: string | null },
  ): Promise<void> {
    const res = await this.gql.mutate(UpdatePlantDocument, {
      input: {
        id: this.plantId(),
        expectedRevision: this.expectedRevision,
        name: v.name.trim(),
        scientificName: blankToNull(v.scientificName),
        cultivar: blankToNull(v.cultivar),
        rootstock: blankToNull(v.rootstock),
        type: v.type,
        count: count.value,
        category: v.category,
        plantedOn: blankToNull(v.plantedOn),
        estimatedAgeYears: age.value,
      },
      clientMutationId: `${this.cmid}:update`,
    });
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind !== 'ok') {
      this.notices.set(['Could not reach the server. Nothing was saved.']);
      return;
    }
    const payload = res.data?.updatePlant;
    const problems = (payload?.problems ?? []) as PayloadProblem[];
    if (problems.length > 0) {
      const note = conflictNote(problems, 'This plant');
      if (note) this.conflict.set(note);
      this.serverErr.set(problemMap(problems));
      this.notices.set(generalProblems(problems));
      return;
    }
    this.toasts.ok('Plant updated');
    // Save approval first: the entry guard must not prompt on the success
    // navigation, and the form is now the saved state.
    this.form.markAsPristine();
    this.entry()?.markPristine();
    await this.router.navigate(['/plants', this.plantId()]);
  }

  private async submitCreate(
    v: ReturnType<typeof this.form.getRawValue>,
    count: { value: number | null },
    age: { value: string | null },
    pot: { value: string | null },
  ): Promise<void> {
    // Optional inline location first — the plant references it once it exists.
    let locationId = blankToNull(v.locationId);
    const newLoc = blankToNull(v.newLocation);
    if (newLoc) {
      const res = await this.gql.mutate(CreateLocationDocument, {
        input: {
          name: newLoc,
          situation: v.newLocationSituation,
          parentLocationId: blankToNull(v.newLocationParent),
        },
        clientMutationId: `${this.cmid}:loc`,
      });
      if (res.kind === 'auth') {
        this.session.markExpired();
        return;
      }
      const problems = (res.kind === 'ok' ? res.data?.createLocation?.problems : []) ?? [];
      if (problems.length > 0) {
        this.serverErr.set({ newLocation: problems[0].message });
        return;
      }
      locationId =
        res.kind === 'ok' ? (res.data?.createLocation?.location?.id ?? locationId) : locationId;
    }

    // Context is only sent when the user actually said something about it:
    // a location, a pot volume, or a non-default placement. Silence stays
    // silence — we never invent a context the user did not describe.
    const hasContext = locationId != null || pot.value != null || v.placement !== 'CONTAINER';
    const hasAcquisition = blankToNull(v.seller) != null || blankToNull(v.acquiredOn) != null;

    const res = await this.gql.mutate(CreatePlantDocument, {
      input: {
        name: v.name.trim(),
        scientificName: blankToNull(v.scientificName),
        cultivar: blankToNull(v.cultivar),
        rootstock: blankToNull(v.rootstock),
        type: v.type,
        count: count.value,
        category: v.category,
        plantedOn: blankToNull(v.plantedOn),
        estimatedAgeYears: age.value,
        tagIds: this.selectedTags().length ? this.selectedTags() : undefined,
        ...(hasContext
          ? {
              initialContext: {
                placement: v.placement,
                locationId,
                potVolumeLiters: pot.value,
              },
            }
          : {}),
        ...(hasAcquisition
          ? {
              acquisition: {
                pricingState: 'UNKNOWN' as const, // pricing is T11; unknown stays honest
                seller: blankToNull(v.seller),
                acquiredOn: blankToNull(v.acquiredOn),
              },
            }
          : {}),
      },
      clientMutationId: `${this.cmid}:create`,
    });
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind !== 'ok') {
      this.notices.set(['Could not reach the server. Nothing was saved.']);
      return;
    }
    const payload = res.data?.createPlant;
    const problems = (payload?.problems ?? []) as PayloadProblem[];
    if (problems.length > 0) {
      this.serverErr.set(problemMap(problems));
      this.notices.set(generalProblems(problems));
      return;
    }
    const plant = payload?.plant;
    this.toasts.ok(`“${v.name.trim()}” added`);
    // Save approval before navigating: a completed save never prompts.
    this.form.markAsPristine();
    this.entry()?.markPristine();
    await this.router.navigate(plant ? ['/plants', plant.id] : ['/plants']);
  }
}
