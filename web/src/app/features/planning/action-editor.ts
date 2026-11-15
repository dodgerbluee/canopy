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
  CreatePlantingActionDocument,
  UpdatePlantingActionDocument,
  DeletePlantingActionDocument,
  type PlantingActionKind,
} from '@canopy/contracts';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { PlanningApi, PlanningAttempt, dateLabel, type Plan } from './planning-data';
import { isCivilDate } from './planning-form-data';

type Action = Plan['actions'][number];
@Component({
  selector: 'planning-action-editor',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './planning-dialogs.css',
  template: `<ui-entry-dialog
    #entry
    [open]="open()"
    [title]="action() ? 'Edit planned action' : 'Add a planned action'"
    description="Choose an intentional next step. This does not record work as completed."
    [busy]="saving()"
    [dirty]="dirty()"
    (dismissed)="dismissed.emit()"
  >
    <form (ngSubmit)="save()" novalidate>
      @if (message()) {
        <p class="form-error" role="alert">{{ message() }}</p>
      }
      <fieldset class="form-section" [disabled]="saving()" style="border:0;padding:0;margin:0">
        <div class="field">
          <label class="field-label" for="action-kind">What is the next step?</label
          ><select class="input" id="action-kind" name="kind" [(ngModel)]="draft.kind">
            <option value="PREPARE">Prepare the space</option>
            <option value="BUY">Buy plants or supplies</option>
            <option value="START_INDOORS">Start seeds indoors</option>
            <option value="DIRECT_SOW">Sow directly</option>
            <option value="TRANSPLANT">Transplant</option>
            <option value="PLANT_OUT">Plant outside</option>
          </select>
        </div>
        <div class="field">
          <label class="field-label" for="action-title">Action title</label
          ><input
            class="input"
            id="action-title"
            name="title"
            [(ngModel)]="draft.title"
            maxlength="160"
            required
            placeholder="Prepare a container, visit the nursery…"
            [attr.aria-invalid]="submitted() && !draft.title.trim() ? 'true' : null"
          />
        </div>
        <div class="field">
          <label class="field-label" for="action-date">Planned day</label
          ><input
            type="date"
            class="input"
            id="action-date"
            name="day"
            [(ngModel)]="draft.day"
            required
            [attr.aria-invalid]="submitted() && !validDay() ? 'true' : null"
          />
          <p class="field-hint">A day you choose, in your garden's timezone.</p>
        </div>
        <div class="field">
          <label class="field-label"
            ><input type="checkbox" name="remind" [(ngModel)]="draft.remind" /> Remind me in
            Inbox</label
          >
          @if (draft.remind) {
            <label class="field-label" for="action-reminder">Remind on</label
            ><input
              class="input"
              id="action-reminder"
              name="reminder"
              type="date"
              [(ngModel)]="draft.reminder"
              [max]="draft.day"
              required
            />
            <p class="field-hint">
              At 9 am in your garden's timezone, on or before the planned day. No email is sent.
            </p>
          }
        </div>
        <div class="field">
          <label class="field-label" for="action-notes">Notes</label
          ><textarea
            class="input"
            id="action-notes"
            name="notes"
            rows="3"
            [(ngModel)]="draft.notes"
            maxlength="20000"
          ></textarea>
        </div>
      </fieldset>
      @if (action() && !removing()) {
        <div class="planning-options">
          <button
            type="button"
            class="btn btn-ghost"
            [disabled]="saving()"
            (click)="removing.set(true)"
          >
            Remove this action
          </button>
        </div>
      }
      @if (removing()) {
        <div class="planning-choice">
          <p>Remove “{{ action()?.title }}” from this plan?</p>
          <small>This removes its planned date. It does not change recorded garden activity.</small>
          <div class="form-actions">
            <button type="button" class="btn" [disabled]="saving()" (click)="removing.set(false)">
              Keep action</button
            ><button type="button" class="btn btn-danger" [disabled]="saving()" (click)="remove()">
              Remove action
            </button>
          </div>
        </div>
      }
      <div class="form-actions">
        <button type="button" class="btn" [disabled]="saving()" (click)="entry.requestClose()">
          Cancel</button
        ><button type="submit" class="btn btn-primary" [disabled]="saving()">
          {{ saving() ? 'Saving…' : action() ? 'Save changes' : 'Add action' }}
        </button>
      </div>
    </form></ui-entry-dialog
  >`,
})
export class ActionEditor {
  private readonly api = inject(PlanningApi);
  readonly open = input(false);
  readonly plan = input<Plan | null>(null);
  readonly action = input<Action | null>(null);
  readonly dismissed = output<void>();
  readonly saved = output<Plan>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly saving = signal(false);
  readonly message = signal('');
  readonly submitted = signal(false);
  readonly removing = signal(false);
  draft = { kind: 'PREPARE', title: '', day: '', notes: '', remind: false, reminder: '' };
  private original = '';
  private attempt = new PlanningAttempt();
  constructor() {
    let wasOpen = false;
    effect(() => {
      const open = this.open();
      untracked(() => {
        if (open && !wasOpen) {
          const a = this.action();
          this.draft = {
            kind: a?.kind ?? 'PREPARE',
            title: a?.title ?? '',
            day: a?.scheduledOn ?? '',
            notes: a?.notes ?? '',
            remind: !!a?.reminderOn,
            reminder: a?.reminderOn ?? a?.scheduledOn ?? '',
          };
          this.original = JSON.stringify(this.draft);
          this.attempt = new PlanningAttempt();
          this.message.set('');
          this.submitted.set(false);
          this.removing.set(false);
        }
        wasOpen = open;
      });
    });
  }
  dirty(): boolean {
    return JSON.stringify(this.draft) !== this.original;
  }
  validDay(): boolean {
    return isCivilDate(this.draft.day);
  }
  async save(): Promise<void> {
    const plan = this.plan();
    if (!plan || this.saving()) return;
    this.submitted.set(true);
    if (!this.draft.title.trim() || !this.validDay()) {
      this.message.set('Enter an action title and a real planned day.');
      return;
    }
    if (
      this.draft.remind &&
      (!isCivilDate(this.draft.reminder) || this.draft.reminder > this.draft.day)
    ) {
      this.message.set('Choose a reminder day on or before the planned day.');
      return;
    }
    this.saving.set(true);
    this.message.set('');
    try {
      const action = this.action();
      const input = {
        planId: plan.id,
        expectedRevision: plan.revision,
        kind: this.draft.kind as PlantingActionKind,
        title: this.draft.title.trim(),
        scheduledOn: this.draft.day,
        notes: this.draft.notes.trim() || null,
        reminderOn: this.draft.remind ? this.draft.reminder : null,
      };
      const payload = action
        ? await this.api.command(
            UpdatePlantingActionDocument,
            {
              input: { ...input, id: action.id },
              clientMutationId: this.attempt.for({ ...input, id: action.id }),
            },
            (d) => d.updatePlantingAction,
          )
        : await this.api.command(
            CreatePlantingActionDocument,
            { input, clientMutationId: this.attempt.for(input) },
            (d) => d.createPlantingAction,
          );
      if (payload.problems.length) {
        this.message.set(
          payload.problems.map((p) => p.message).join(' ') +
            ' Reload the plan before trying again if it changed elsewhere.',
        );
        return;
      }
      if (!payload.plan)
        throw new Error('The server did not return the saved action. Retry with the same values.');
      this.entry()?.markPristine();
      this.saved.emit(payload.plan);
    } catch (e) {
      this.message.set((e as Error).message);
    } finally {
      this.saving.set(false);
    }
  }
  async remove(): Promise<void> {
    const plan = this.plan(),
      action = this.action();
    if (!plan || !action || this.saving()) return;
    this.saving.set(true);
    this.message.set('');
    try {
      const input = { planId: plan.id, id: action.id, expectedRevision: plan.revision };
      const payload = await this.api.command(
        DeletePlantingActionDocument,
        { input, clientMutationId: this.attempt.for(input) },
        (d) => d.deletePlantingAction,
      );
      if (payload.problems.length) {
        this.message.set(payload.problems.map((p) => p.message).join(' '));
        return;
      }
      if (!payload.plan) throw new Error('Could not confirm removal. Please retry.');
      this.entry()?.markPristine();
      this.saved.emit(payload.plan);
    } catch (e) {
      this.message.set((e as Error).message);
    } finally {
      this.saving.set(false);
    }
  }
}
