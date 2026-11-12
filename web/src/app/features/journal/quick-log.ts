import { requestId } from '../../core/request-id';
import {
  afterNextRender,
  ChangeDetectionStrategy,
  Component,
  Injector,
  OnInit,
  inject,
  input,
  output,
  signal,
} from '@angular/core';
import {
  PlantsDocument,
  RecordActivityDocument,
  RecordMeasurementDocument,
  UpdateActivityDocument,
  UpdateMeasurementDocument,
  type ActivityKind,
  type MeasurementMethod,
  type QuantityKind,
  type Unit,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { SessionService } from '../../core/auth/session';
import { UiToasts } from '../../ui/toast';
import {
  blankToNull,
  conflictNote,
  generalProblems,
  parseDecimal,
  type PayloadProblem,
} from '../collection/shared';
import {
  browserTimezone,
  localDateInput,
  localInstant,
  localTimeInput,
  type ActivityEntry,
  type JournalItem,
  type MeasurementEntry,
} from './journal-view';

const ACTIVITY_KINDS: { value: ActivityKind; label: string }[] = [
  { value: 'WATER', label: 'Water' },
  { value: 'FERTILIZE', label: 'Fertilize' },
  { value: 'PRUNE', label: 'Prune' },
  { value: 'REPOT', label: 'Repot' },
  { value: 'HARVEST', label: 'Harvest' },
  { value: 'OBSERVE', label: 'Observe' },
  { value: 'NOTE', label: 'Note' },
];
const MEASURE_KINDS: { value: QuantityKind; label: string }[] = [
  { value: 'LENGTH', label: 'Length' },
  { value: 'MASS', label: 'Mass' },
  { value: 'VOLUME', label: 'Volume' },
  { value: 'COUNT', label: 'Count' },
  { value: 'TEMPERATURE', label: 'Temperature' },
];
const METHODS: { value: MeasurementMethod; label: string }[] = [
  { value: 'MANUAL', label: 'Manual' },
  { value: 'IMAGE_ESTIMATE', label: 'From photo' },
  { value: 'SENSOR', label: 'Sensor' },
];
/** Unit sets per the frozen unit-compatibility rules (T17). */
const UNITS_BY_QK: Record<QuantityKind, Unit[]> = {
  LENGTH: ['CM', 'MM'],
  MASS: ['G', 'KG'],
  VOLUME: ['L', 'ML'],
  COUNT: ['COUNT'],
  TEMPERATURE: ['C', 'F'],
};
const WATER_UNITS: Unit[] = ['L', 'ML'];
const FERTILIZE_UNITS: Unit[] = ['G', 'KG', 'L', 'ML'];

/**
 * Quick log (T19): one compact form for logging an activity or a measurement
 * against a plant — from a phone, in seconds. Backdating is explicit (date +
 * optional time, browser zone travels); unknown optionals stay null rather
 * than zero-filled. In edit mode it corrects an existing record through the
 * revision-guarded update mutations; kind and unit are frozen after creation
 * (corrections change values, not identity).
 */
@Component({
  selector: 'quick-log',
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './quick-log.html',
  styleUrl: './quick-log.css',
})
export class QuickLog implements OnInit {
  /** Fixed plant when set; null offers a workspace plant picker. */
  readonly plantId = input<string | null>(null);
  /** Set by the timeline to correct an existing activity/measurement. */
  readonly editing = input<JournalItem | null>(null);
  /** Initial creation kind; editing still follows the stored record. */
  readonly initialKind = input<ActivityKind>('NOTE');
  readonly saved = output<void>();
  readonly cancelled = output<void>();
  /** Save in flight, surfaced so a modal host can bind it to UiEntryDialog
   * [busy] and block dismissals/route leave while a mutation runs. */
  readonly busyChange = output<boolean>();
  /** A revision CONFLICT was explained and the record reloaded: the host
   * drops the stale draft instead of prompting to discard it. */
  readonly conflict = output<void>();

  private readonly gql = inject(GraphQLClient);
  private readonly session = inject(SessionService);
  private readonly toasts = inject(UiToasts);
  private readonly injector = inject(Injector);

  protected readonly activityKinds = ACTIVITY_KINDS;
  protected readonly measureKinds = MEASURE_KINDS;
  protected readonly methods = METHODS;
  protected readonly today = new Date().toISOString().slice(0, 10);

  protected readonly mode = signal<'activity' | 'measurement'>('activity');
  protected readonly kind = signal<ActivityKind>('NOTE');
  protected readonly qKind = signal<QuantityKind>('LENGTH');
  protected readonly busy = signal(false);
  /** per-mode drafts, so switching activity/measurement keeps what was typed */
  private readonly drafts = new Map<'activity' | 'measurement', FormData>();
  protected readonly formError = signal('');
  protected readonly plants = signal<{ id: string; name: string }[]>([]);

  // Inputs are only bound after the constructor — all input-derived state
  // initializes in ngOnInit.
  ngOnInit(): void {
    const e = this.editing();
    this.kind.set(this.initialKind());
    if (e) {
      this.mode.set(e.__typename === 'ActivityEntry' ? 'activity' : 'measurement');
      if (e.__typename === 'ActivityEntry') this.kind.set(e.activity.kind);
      if (e.__typename === 'MeasurementEntry') this.qKind.set(e.measurement.quantityKind);
    } else if (this.plantId() == null) {
      // default filter: ACTIVE + DEAD visible, archived hidden — exactly the
      // plants a log entry can legitimately target.
      void this.loadPlants();
    }
  }

  protected plantPicker(): boolean {
    return this.plantId() == null && !this.editing();
  }

  protected editEntry(): JournalItem | null {
    return this.editing();
  }

  protected a(): ActivityEntry['activity'] {
    const e = this.editing();
    return e?.__typename === 'ActivityEntry' ? e.activity : EMPTY_ACTIVITY.activity;
  }
  protected m(): MeasurementEntry['measurement'] {
    const e = this.editing();
    return e?.__typename === 'MeasurementEntry' ? e.measurement : EMPTY_MEASUREMENT.measurement;
  }
  protected aOccurred(): string {
    return this.a().occurredAt;
  }
  protected mObserved(): string {
    return this.m().observedAt;
  }
  protected mUnit(): Unit {
    return this.m().unit;
  }

  protected needsQuantity(): boolean {
    return this.kind() === 'WATER' || this.kind() === 'FERTILIZE';
  }
  protected quantityUnits(): Unit[] {
    return this.kind() === 'WATER' ? WATER_UNITS : FERTILIZE_UNITS;
  }
  protected measureUnits(): Unit[] {
    return UNITS_BY_QK[this.qKind()];
  }
  protected pickQK(ev: Event): void {
    this.qKind.set((ev.target as HTMLSelectElement).value as QuantityKind);
  }

  /** Mode switch without losing what was typed: the departing mode's fields
   * are cached, and the destination mode's draft is restored after its block
   * renders. plantId lives outside the mode blocks and keeps its latest value. */
  protected setMode(next: 'activity' | 'measurement', form: HTMLFormElement): void {
    if (this.mode() === next || this.busy()) return;
    this.drafts.set(this.mode(), new FormData(form));
    this.mode.set(next);
    const draft = this.drafts.get(next);
    if (!draft) return;
    afterNextRender(
      () => {
        for (const [name, value] of draft.entries()) {
          if (name === 'plantId') continue;
          const field = form.elements.namedItem(name);
          if (
            field instanceof HTMLInputElement ||
            field instanceof HTMLSelectElement ||
            field instanceof HTMLTextAreaElement
          ) {
            field.value = String(value);
          }
        }
      },
      { injector: this.injector },
    );
  }

  protected localDateInput = localDateInput;
  protected localTimeInput = localTimeInput;

  protected cancelEdit(): void {
    this.cancelled.emit();
  }

  /** Every busy transition travels out, so a modal host can bind [busy]. */
  private setBusy(next: boolean): void {
    if (this.busy() === next) return;
    this.busy.set(next);
    this.busyChange.emit(next);
  }

  private async loadPlants(): Promise<void> {
    const res = await this.gql.query(PlantsDocument, { first: 100 });
    if (res.kind === 'auth') this.session.markExpired();
    if (res.kind === 'ok' && res.data) {
      this.plants.set(res.data.plants.items.map((p) => ({ id: p.id, name: p.name })));
    }
  }

  protected onSubmit(ev: Event, el: HTMLFormElement): void {
    ev.preventDefault();
    void this.submit(el);
  }

  protected async submit(el: HTMLFormElement): Promise<void> {
    if (this.busy()) return;
    this.formError.set('');
    const d = Object.fromEntries(new FormData(el));
    const instant = localInstant(String(d['date'] ?? ''), String(d['time'] ?? ''));
    if (!instant) {
      this.formError.set('Pick a real date; time is optional (blank means local midnight).');
      return;
    }
    if (this.editing()) {
      await this.submitEdit(d, instant);
    } else if (this.mode() === 'activity') {
      await this.submitActivity(d, instant);
    } else {
      await this.submitMeasurement(d, instant);
    }
  }

  private targetPlant(d: Record<string, unknown>): string | null {
    const fixed = this.plantId();
    if (fixed) return fixed;
    return blankToNull(String(d['plantId'] ?? ''));
  }

  private async submitActivity(d: Record<string, unknown>, occurredAt: string): Promise<void> {
    const plantId = this.targetPlant(d);
    if (!plantId) {
      this.formError.set('Choose the plant this happened to.');
      return;
    }
    const kind = (String(d['kind']) as ActivityKind) || 'NOTE';
    const q = parseDecimal(String(d['quantity'] ?? ''));
    if (q.error) {
      this.formError.set(q.error);
      return;
    }
    const quantity = q.value;
    const unit = blankToNull(String(d['unit'] ?? '')) as Unit | null;
    if ((kind === 'WATER' || kind === 'FERTILIZE') && quantity !== null && unit === null) {
      this.formError.set(
        'Quantity needs a unit — litres/millilitres for water, mass or volume for fertilizer.',
      );
      return;
    }
    this.setBusy(true);
    try {
      const res = await this.gql.mutate(RecordActivityDocument, {
        input: {
          plantId,
          kind,
          occurredAt,
          timezone: browserTimezone(),
          note: blankToNull(String(d['note'] ?? '')),
          ...(kind === 'WATER' || kind === 'FERTILIZE'
            ? {
                quantity,
                unit,
                product: kind === 'FERTILIZE' ? blankToNull(String(d['product'] ?? '')) : null,
              }
            : {}),
        },
        clientMutationId: `${requestId()}:record-activity`,
      });
      this.finish(res, 'recordActivity', 'Logged');
    } finally {
      this.setBusy(false);
    }
  }

  private async submitMeasurement(d: Record<string, unknown>, observedAt: string): Promise<void> {
    const plantId = this.targetPlant(d);
    if (!plantId) {
      this.formError.set('Choose the plant you measured.');
      return;
    }
    const v = parseDecimal(String(d['value'] ?? ''));
    if (v.error || v.value === null) {
      this.formError.set(v.error ?? 'A value is required — measurements are never guessed.');
      return;
    }
    const unc = parseDecimal(String(d['uncertainty'] ?? ''));
    if (unc.error) {
      this.formError.set(unc.error);
      return;
    }
    this.setBusy(true);
    try {
      const res = await this.gql.mutate(RecordMeasurementDocument, {
        input: {
          plantId,
          quantityKind: (String(d['quantityKind']) as QuantityKind) || this.qKind(),
          unit: (String(d['unit']) as Unit) || UNITS_BY_QK[this.qKind()][0],
          value: v.value,
          observedAt,
          method: (String(d['method']) as MeasurementMethod) || 'MANUAL',
          uncertainty: unc.value,
        },
        clientMutationId: `${requestId()}:record-measurement`,
      });
      this.finish(res, 'recordMeasurement', 'Measurement recorded');
    } finally {
      this.setBusy(false);
    }
  }

  private async submitEdit(d: Record<string, unknown>, occurredAt: string): Promise<void> {
    const e = this.editing();
    if (!e) return;
    this.setBusy(true);
    try {
      if (e.__typename === 'ActivityEntry') {
        const q = parseDecimal(String(d['quantity'] ?? ''));
        if (q.error) {
          this.formError.set(q.error);
          return;
        }
        const quantity = q.value;
        const unit = blankToNull(String(d['unit'] ?? '')) as Unit | null;
        const kind = e.activity.kind;
        const res = await this.gql.mutate(UpdateActivityDocument, {
          input: {
            id: e.activity.id,
            expectedRevision: e.activity.revision,
            occurredAt,
            timezone: browserTimezone(),
            note: blankToNull(String(d['note'] ?? '')),
            ...(kind === 'WATER' || kind === 'FERTILIZE'
              ? {
                  quantity,
                  unit,
                  product: kind === 'FERTILIZE' ? blankToNull(String(d['product'] ?? '')) : null,
                }
              : {}),
          },
          clientMutationId: `${requestId()}:update-activity`,
        });
        this.finish(res, 'updateActivity', 'Activity corrected');
      } else if (e.__typename === 'MeasurementEntry') {
        const v = parseDecimal(String(d['value'] ?? ''));
        if (v.error || v.value === null) {
          this.formError.set(v.error ?? 'A value is required — measurements are never guessed.');
          return;
        }
        const unc = parseDecimal(String(d['uncertainty'] ?? ''));
        if (unc.error) {
          this.formError.set(unc.error);
          return;
        }
        const res = await this.gql.mutate(UpdateMeasurementDocument, {
          input: {
            id: e.measurement.id,
            expectedRevision: e.measurement.revision,
            observedAt: occurredAt,
            value: v.value,
            uncertainty: unc.value,
          },
          clientMutationId: `${requestId()}:update-measurement`,
        });
        this.finish(res, 'updateMeasurement', 'Measurement corrected');
      }
    } finally {
      this.setBusy(false);
    }
  }

  /** Payload problems are results: map field problems onto the form and
   * explain conflicts, then let the timeline reload the truth. */
  private finish(res: GqlResult<unknown>, field: string, okLabel: string): void {
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind !== 'ok') {
      this.formError.set('Could not reach the server. Nothing was changed.');
      return;
    }
    const payload = (res.data as Record<string, { problems?: PayloadProblem[] } | null> | null)?.[
      field
    ];
    const problems = payload?.problems ?? [];
    if (problems.length > 0) {
      const note =
        conflictNote(problems, 'This record') ||
        generalProblems(problems).join(' ') ||
        problems[0].message;
      if (problems.some((p) => p.code === 'CONFLICT')) {
        // the record moved under us: explain, then let the timeline reload truth
        this.toasts.error(note);
        this.conflict.emit();
        this.saved.emit();
      } else {
        this.formError.set(note); // fixable field problem — keep the form open
      }
      return;
    }
    this.toasts.ok(okLabel);
    this.saved.emit();
  }
}

const EMPTY_ACTIVITY = {
  __typename: 'ActivityEntry',
  id: '',
  plantId: '',
  occurredAt: new Date().toISOString(),
  createdAt: '',
  summary: '',
  tombstonedAt: null,
  activity: {
    id: '',
    plantId: '',
    kind: 'NOTE' as ActivityKind,
    occurredAt: new Date().toISOString(),
    timezone: 'UTC',
    note: null,
    quantity: null,
    unit: null,
    product: null,
    revision: 0,
    voidedAt: null,
    createdAt: '',
    updatedAt: '',
  },
} satisfies ActivityEntry;

const EMPTY_MEASUREMENT = {
  __typename: 'MeasurementEntry',
  id: '',
  plantId: '',
  occurredAt: new Date().toISOString(),
  createdAt: '',
  summary: '',
  tombstonedAt: null,
  measurement: {
    id: '',
    plantId: '',
    quantityKind: 'LENGTH' as QuantityKind,
    value: '',
    unit: 'CM' as Unit,
    observedAt: new Date().toISOString(),
    method: 'MANUAL' as MeasurementMethod,
    uncertainty: null,
    activityId: null,
    revision: 0,
    createdAt: '',
    updatedAt: '',
  },
} satisfies MeasurementEntry;
