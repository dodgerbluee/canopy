import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
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
  CompletePlantingActionDocument,
  DismissPlantingActionDocument,
  type PlantFieldsFragment,
  type ContextPlacement,
} from '@canopy/contracts';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { workspaceDay } from '../calendar/calendar-data';
import { PlanningApi, PlanningAttempt, type Plan } from './planning-data';
import { isCivilDate } from './planning-form-data';
export function needsLivingPlant(kind: string): boolean {
  return ['START_INDOORS', 'DIRECT_SOW', 'TRANSPLANT', 'PLANT_OUT'].includes(kind);
}
@Component({
  selector: 'planning-action-completion',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './planning-dialogs.css',
  template: ` <ui-entry-dialog
    #entry
    [open]="open()"
    [title]="dismissing() ? 'Dismiss planned action' : 'Complete planned action'"
    [description]="action()?.title ?? ''"
    [busy]="saving()"
    [dirty]="dirty()"
    (dismissed)="dismissed.emit()"
  >
    <form (ngSubmit)="save()" novalidate>
      @if (message()) {
        <p class="form-error" role="alert">{{ message() }}</p>
      }
      @if (loading()) {
        <p role="status">Reading the linked plant…</p>
      } @else if (!dismissing()) {
        <fieldset class="form-section" [disabled]="saving()" style="border:0;padding:0;margin:0">
          <div class="field">
            <label class="field-label" for="complete-day">Actually completed on</label
            ><input
              class="input"
              id="complete-day"
              name="day"
              type="date"
              [(ngModel)]="draft.day"
              [max]="today()"
              required
            />
          </div>
          @if (isPlanting()) {
            <p class="planning-note">
              This updates {{ plant()?.name || 'the linked plant' }}'s actual planting date. Your
              chosen plan window stays unchanged.
            </p>
            <label
              ><input
                name="placementConfirmed"
                type="checkbox"
                [(ngModel)]="draft.confirmContext"
              />
              Record the actual placement at planting</label
            >
            @if (draft.confirmContext) {
              <div class="field">
                <label class="field-label" for="complete-placement">Actual placement</label
                ><select
                  class="input"
                  id="complete-placement"
                  name="placement"
                  [(ngModel)]="draft.placement"
                >
                  <option value="">Choose placement…</option>
                  <option value="CONTAINER">Container</option>
                  <option value="GROUND">In the ground</option>
                </select>
              </div>
              <div class="field">
                <label class="field-label" for="complete-location">Actual location</label
                ><select
                  class="input"
                  id="complete-location"
                  name="location"
                  [(ngModel)]="draft.locationId"
                >
                  <option value="">Not recorded</option>
                  @for (location of locations(); track location.id) {
                    <option [value]="location.id">{{ location.name }}</option>
                  }
                </select>
              </div>
            }
          } @else {
            <p class="planning-note">
              This completes a planning step. It does not create a plant, purchase, watering or
              other garden log.
            </p>
          }
        </fieldset>
      } @else {
        <div class="planning-choice">
          <p>Dismiss “{{ action()?.title }}”?</p>
          <small
            >This closes the planned action without claiming it happened or changing the linked
            plant.</small
          >
        </div>
      }
      @if (loadError() && !dismissing()) {
        <button class="btn" type="button" (click)="loadPlant()">Reload linked plant</button>
      }
      <div class="form-actions">
        <button class="btn" type="button" [disabled]="saving()" (click)="entry.requestClose()">
          Cancel</button
        ><button
          class="btn btn-primary"
          type="submit"
          [disabled]="saving() || loading() || (!dismissing() && !!loadError())"
        >
          {{ saving() ? 'Saving…' : dismissing() ? 'Dismiss action' : 'Mark complete' }}
        </button>
      </div>
    </form>
  </ui-entry-dialog>`,
})
export class ActionCompletion {
  private readonly api = inject(PlanningApi);
  readonly open = input(false);
  readonly plan = input<Plan | null>(null);
  readonly action = input<Plan['actions'][number] | null>(null);
  readonly dismissOnly = input(false);
  readonly timezone = input('UTC');
  readonly locations = input<Awaited<ReturnType<PlanningApi['context']>>['locations']>([]);
  readonly dismissed = output<void>();
  readonly saved = output<Plan>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly saving = signal(false);
  readonly loading = signal(false);
  readonly loadError = signal('');
  readonly message = signal('');
  readonly dismissing = signal(false);
  readonly plant = signal<PlantFieldsFragment | null>(null);
  readonly today = signal('');
  draft = { day: '', confirmContext: false, placement: '', locationId: '' };
  private original = '';
  private attempt = new PlanningAttempt();
  private abort?: AbortController;
  private generation = 0;
  constructor() {
    let wasOpen = false;
    effect(() => {
      const open = this.open();
      untracked(() => {
        if (open && !wasOpen) {
          this.today.set(workspaceDay(new Date(), this.timezone()));
          this.draft = { day: this.today(), confirmContext: false, placement: '', locationId: '' };
          this.original = JSON.stringify(this.draft);
          this.message.set('');
          this.loadError.set('');
          this.dismissing.set(this.dismissOnly());
          this.plant.set(null);
          this.attempt = new PlanningAttempt();
          if (this.isPlanting() && !this.dismissing()) void this.loadPlant();
        }
        if (!open) {
          this.generation++;
          this.abort?.abort();
        }
        wasOpen = open;
      });
    });
    inject(DestroyRef).onDestroy(() => {
      this.generation++;
      this.abort?.abort();
    });
  }
  dirty(): boolean {
    return JSON.stringify(this.draft) !== this.original;
  }
  isPlanting(): boolean {
    return needsLivingPlant(this.action()?.kind ?? '');
  }
  async loadPlant(): Promise<void> {
    const id = this.plan()?.fulfillment?.plant?.id;
    if (!id) {
      this.loadError.set('The linked plant is unavailable.');
      return;
    }
    const n = ++this.generation;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.loading.set(true);
    this.loadError.set('');
    this.message.set('');
    try {
      const plant = await this.api.actualPlant(id, abort.signal);
      if (n === this.generation) {
        if (plant.status !== 'ACTIVE')
          throw new Error('Restore the actual plant before recording its planting.');
        this.plant.set(plant);
      }
    } catch (e) {
      if (n === this.generation) {
        this.loadError.set((e as Error).message);
        this.message.set(this.loadError());
      }
    } finally {
      if (n === this.generation) this.loading.set(false);
    }
  }
  async save(): Promise<void> {
    const plan = this.plan(),
      action = this.action();
    if (!plan || !action || this.saving() || this.loading()) return;
    this.message.set('');
    try {
      if (!this.dismissing()) {
        if (!isCivilDate(this.draft.day) || this.draft.day > this.today())
          throw new Error('Choose an actual completion day on or before today.');
        if (this.isPlanting() && !this.plant())
          throw new Error('Reload the linked plant before confirming.');
        if (this.draft.confirmContext && !['GROUND', 'CONTAINER'].includes(this.draft.placement))
          throw new Error('Choose the actual placement.');
      }
      const base = { planId: plan.id, id: action.id, expectedRevision: plan.revision };
      this.saving.set(true);
      const input = {
        ...base,
        completedOn: this.draft.day,
        expectedPlantRevision: this.isPlanting() ? this.plant()?.revision : null,
        actualContext:
          this.isPlanting() && this.draft.confirmContext
            ? {
                placement: this.draft.placement as ContextPlacement,
                locationId: this.draft.locationId || null,
              }
            : null,
      };
      const payload = this.dismissing()
        ? await this.api.command(
            DismissPlantingActionDocument,
            { input: base, clientMutationId: this.attempt.for({ dismiss: base }) },
            (d) => d.dismissPlantingAction,
          )
        : await this.api.command(
            CompletePlantingActionDocument,
            { input, clientMutationId: this.attempt.for(input) },
            (d) => d.completePlantingAction,
          );
      if (payload.problems.length) {
        this.message.set(
          payload.problems.map((p) => p.message).join(' ') +
            ' Reload before trying again if the plan or actual plant changed.',
        );
        return;
      }
      if (!payload.plan)
        throw new Error('The change could not be confirmed. Retry the same values safely.');
      this.entry()?.markPristine();
      this.saved.emit(payload.plan);
    } catch (e) {
      this.message.set((e as Error).message);
    } finally {
      this.saving.set(false);
    }
  }
}
