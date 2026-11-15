import {
  ChangeDetectionStrategy,
  Component,
  effect,
  inject,
  input,
  output,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  ConfirmPlantingScheduleDocument,
  type PlantingPeriodInput,
  type PlantingScheduleAssumptionsInput,
  type PlantingTimingAnchor,
} from '@canopy/contracts';
import { UiEntryDialog } from '../../ui/entry-dialog';
import {
  PlanningApi,
  PlanningAttempt,
  periodLabel,
  type Detail,
  type Plan,
  type ProjectedWindow,
} from './planning-data';
import { isCivilDate } from './planning-form-data';

@Component({
  selector: 'planning-window-chooser',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './planning-dialogs.css',
  template: `<ui-entry-dialog
    #entry
    [open]="open()"
    title="Review a growing window"
    description="Compare the source with your plans, then choose what works for you."
    [busy]="busy()"
    [dirty]="dirty()"
    (dismissed)="dismissed.emit()"
  >
    @if (fact(); as f) {
      <div class="planning-choice">
        <strong>{{ f.label }}</strong>
        <p>{{ f.summary }}</p>
        <a class="planning-source" [href]="f.sourceUrl" target="_blank" rel="noopener noreferrer"
          >{{ f.publisher }} · {{ f.sourceTitle }} ↗</a
        ><small>{{ f.sourceRegion }} · Retrieved {{ f.retrievedAt.slice(0, 10) }}</small>
        <details>
          <summary>Conditions &amp; source scope</summary>
          <ul class="planning-conditions">
            @for (c of f.conditions; track c) {
              <li>{{ c }}</li>
            }
          </ul>
        </details>
      </div>
    }
    @if (message()) {
      <p class="form-error" role="alert">{{ message() }}</p>
    }
    @if (current(); as w) {
      <div>
        <p class="eyebrow">{{ w.estimated ? 'PLANNING ESTIMATE' : 'SOURCE WINDOW' }}</p>
        <p>{{ w.period ? periodLabel(w.period) : w.message }}</p>
        @if (w.firstHarvestUnknown) {
          <p class="planning-note">
            This is seasonal harvest guidance. It does not establish when a new planting will first
            produce.
          </p>
        }
      </div>
      @if (anchorKind()) {
        <div class="field">
          <label class="field-label" for="window-anchor">Assumed {{ anchorLabel() }} date</label
          ><input type="date" class="input" id="window-anchor" [(ngModel)]="anchorDate" />
          <p class="field-hint">A planning assumption you provide, not an observed event.</p>
        </div>
      }
      @if (w.resolution === 'NEEDS_HEMISPHERE') {
        <div class="field">
          <label class="field-label" for="window-hemisphere">Intended hemisphere</label
          ><select class="input" id="window-hemisphere" [(ngModel)]="hemisphere">
            <option value="">Choose a hemisphere</option>
            <option value="NORTHERN">Northern</option>
            <option value="SOUTHERN">Southern</option>
          </select>
        </div>
      }
      @if (anchorKind() || w.resolution === 'NEEDS_HEMISPHERE') {
        <button type="button" class="btn" [disabled]="busy()" (click)="preview()">
          {{ busy() ? 'Checking…' : 'Preview with these assumptions' }}
        </button>
      }
      @if (w.selectable && w.period) {
        <form (ngSubmit)="confirm()" novalidate>
          <div class="form-section">
            <div class="field">
              <label class="field-label" for="window-choice">Your choice</label
              ><select class="input" id="window-choice" name="choice" [(ngModel)]="specific">
                <option [ngValue]="false">Keep the source window's precision</option>
                <option [ngValue]="true">Choose specific days inside it</option>
              </select>
            </div>
            @if (specific) {
              <div class="form-grid">
                <div class="field">
                  <label class="field-label" for="window-from">From day</label
                  ><input
                    class="input"
                    type="date"
                    id="window-from"
                    name="from"
                    [(ngModel)]="fromDay"
                  />
                </div>
                <div class="field">
                  <label class="field-label" for="window-to">Through day</label
                  ><input class="input" type="date" id="window-to" name="to" [(ngModel)]="toDay" />
                </div>
              </div>
            }
            <p class="planning-note">
              Choosing saves a target for this plan. It does not create actions, record planting, or
              add a plant to your garden.
            </p>
          </div>
          <div class="form-actions">
            <button type="button" class="btn" [disabled]="busy()" (click)="entry.requestClose()">
              Cancel</button
            ><button
              type="submit"
              class="btn btn-primary"
              [disabled]="busy() || assumptionsChanged()"
            >
              {{ busy() ? 'Saving…' : 'Choose this window' }}
            </button>
          </div>
        </form>
      } @else {
        <div class="form-actions">
          <button type="button" class="btn" [disabled]="busy()" (click)="entry.requestClose()">
            Done
          </button>
        </div>
      }
    }
  </ui-entry-dialog>`,
})
export class WindowChooser {
  private readonly api = inject(PlanningApi);
  readonly open = input(false);
  readonly plan = input<Detail | null>(null);
  readonly window = input<ProjectedWindow | null>(null);
  readonly priorAssumptions = input<PlantingScheduleAssumptionsInput | null>(null);
  readonly fromMonth = input.required<string>();
  readonly toMonth = input.required<string>();
  readonly dismissed = output<void>();
  readonly saved = output<Plan>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly current = signal<ProjectedWindow | null>(null);
  readonly busy = signal(false);
  readonly message = signal('');
  readonly periodLabel = periodLabel;
  specific = false;
  fromDay = '';
  toDay = '';
  anchorDate = '';
  hemisphere = '';
  private initial = '';
  private previewAssumptions = '';
  private attempt = new PlanningAttempt();
  private assumptions: PlantingScheduleAssumptionsInput = { anchors: [] };
  constructor() {
    let wasOpen = false;
    effect(() => {
      const open = this.open();
      untracked(() => {
        if (open && !wasOpen) {
          this.current.set(this.window());
          this.message.set('');
          this.specific = false;
          this.fromDay = '';
          this.toDay = '';
          const prior = this.priorAssumptions();
          this.assumptions = {
            anchors: (prior?.anchors ?? []).map((anchor) => ({ kind: anchor.kind, on: anchor.on })),
            hemisphere: prior?.hemisphere ?? null,
          };
          this.anchorDate =
            this.assumptions.anchors?.find((anchor) => anchor.kind === this.anchorKind())?.on ?? '';
          this.hemisphere = this.assumptions.hemisphere ?? '';
          this.initial = this.state();
          this.previewAssumptions = this.assumptionsState();
          this.attempt = new PlanningAttempt();
        }
        wasOpen = open;
      });
    });
  }
  fact() {
    return this.plan()?.latestGuidance?.facts.find((f) => f.id === this.current()?.factId) ?? null;
  }
  anchorKind(): string {
    const kind = this.fact()?.windows[this.current()?.windowIndex ?? 0]?.relative?.anchor ?? '';
    return kind === 'UNKNOWN' || this.current()?.resolution === 'NEEDS_OFFSET' ? '' : kind;
  }
  anchorLabel(): string {
    return this.anchorKind().toLowerCase().replaceAll('_', ' ');
  }
  private state(): string {
    return JSON.stringify([
      this.specific,
      this.fromDay,
      this.toDay,
      this.anchorDate,
      this.hemisphere,
    ]);
  }
  private assumptionsState(): string {
    return JSON.stringify([this.anchorDate, this.hemisphere]);
  }
  dirty(): boolean {
    return this.state() !== this.initial;
  }
  assumptionsChanged(): boolean {
    return this.previewAssumptions !== this.assumptionsState();
  }
  async preview(): Promise<void> {
    const plan = this.plan(),
      w = this.current();
    if (!plan || !w || this.busy()) return;
    if (this.anchorKind() && !isCivilDate(this.anchorDate)) {
      this.message.set('Enter a real date for the named anchor.');
      return;
    }
    const assumptions: PlantingScheduleAssumptionsInput = {
      anchors: this.anchorKind()
        ? [{ kind: this.anchorKind() as PlantingTimingAnchor, on: this.anchorDate }]
        : [],
      hemisphere: (this.hemisphere || null) as 'NORTHERN' | 'SOUTHERN' | null,
    };
    this.busy.set(true);
    this.message.set('');
    try {
      const page = await this.api.schedule(plan.id, {
        fromMonth: this.fromMonth(),
        toMonth: this.toMonth(),
        assumptions,
      });
      const year = this.anchorKind() ? +this.anchorDate.slice(0, 4) : w.year;
      const found = page.items.find(
        (v) => v.factId === w.factId && v.windowIndex === w.windowIndex && v.year === year,
      );
      if (!found)
        throw new Error(
          'This window is not visible with those assumptions. Adjust the planning horizon and review it again.',
        );
      this.current.set(found);
      this.assumptions = assumptions;
      this.previewAssumptions = this.assumptionsState();
    } catch (e) {
      this.message.set((e as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
  async confirm(): Promise<void> {
    const plan = this.plan(),
      w = this.current();
    if (!plan || !w?.period || !w.selectable || this.busy() || this.assumptionsChanged()) return;
    if (
      this.specific &&
      (!isCivilDate(this.fromDay) || !isCivilDate(this.toDay) || this.fromDay > this.toDay)
    ) {
      this.message.set('Choose real start and end days in order.');
      return;
    }
    const targetPeriod: PlantingPeriodInput = this.specific
      ? { precision: 'DATE_RANGE', dates: { from: this.fromDay, to: this.toDay } }
      : {
          precision: w.period.precision,
          dates: w.period.dates ? { from: w.period.dates.from, to: w.period.dates.to } : null,
          months: w.period.months ? { from: w.period.months.from, to: w.period.months.to } : null,
          season: w.period.season
            ? { season: w.period.season.season, year: w.period.season.year }
            : null,
          year: w.period.year,
        };
    const input = {
      planId: plan.id,
      expectedRevision: plan.revision,
      snapshotId: plan.latestGuidance!.id,
      generation: plan.guidanceGeneration,
      factId: w.factId,
      windowIndex: w.windowIndex,
      year: w.year,
      assumptions: this.assumptions,
      targetPeriod,
    };
    this.busy.set(true);
    this.message.set('');
    try {
      const payload = await this.api.command(
        ConfirmPlantingScheduleDocument,
        { input, clientMutationId: this.attempt.for(input) },
        (d) => d.confirmPlantingSchedule,
      );
      if (payload.problems.length) {
        this.message.set(payload.problems.map((p) => p.message).join(' '));
        return;
      }
      if (!payload.plan)
        throw new Error('Could not confirm the selected window. Retry with the same values.');
      this.entry()?.markPristine();
      this.saved.emit(payload.plan);
    } catch (e) {
      this.message.set((e as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
}
