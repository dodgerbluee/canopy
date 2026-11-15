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
  CreatePlantingPlanDocument,
  UpdatePlantingPlanDocument,
  type UpdatePlantingPlanInput,
} from '@canopy/contracts';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { problemMap } from '../collection/shared';
import { PlanningApi, PlanningAttempt, type Plan } from './planning-data';
import { draftIntent, planDraft } from './planning-form-data';

@Component({
  selector: 'planning-plan-editor',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './plan-editor.html',
  styleUrl: './planning-dialogs.css',
})
export class PlanEditor {
  private readonly api = inject(PlanningApi);
  readonly open = input(false);
  readonly plan = input<Plan | null>(null);
  readonly currency = input('USD');
  readonly idea = input(false);
  readonly suggestedNote = input<{ text: string; expectedRevision: number } | null>(null);
  readonly locations = input<Awaited<ReturnType<PlanningApi['context']>>['locations']>([]);
  readonly dismissed = output<void>();
  readonly saved = output<Plan>();
  readonly cancelRequested = output<Plan>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly saving = signal(false);
  readonly message = signal('');
  readonly conflict = signal(false);
  readonly submitted = signal(false);
  draft = planDraft(null, 'USD');
  private original = '';
  private attempt = new PlanningAttempt();
  private errors: Record<string, string> = {};
  private touched = new Set<string>();
  private revision = 0;
  constructor() {
    let wasOpen = false;
    effect(() => {
      const open = this.open();
      untracked(() => {
        if (open && !wasOpen) this.reset(this.plan());
        wasOpen = open;
      });
    });
  }
  private reset(plan: Plan | null): void {
    this.draft = planDraft(plan, this.currency(), this.idea());
    this.original = JSON.stringify(this.draft);
    const suggestion = this.suggestedNote();
    if (plan && suggestion && suggestion.expectedRevision === plan.revision)
      this.draft.notes = [this.draft.notes, suggestion.text].filter(Boolean).join('\n\n');
    this.revision = plan?.revision ?? 0;
    this.attempt = new PlanningAttempt();
    this.errors = {};
    this.touched.clear();
    this.message.set('');
    this.conflict.set(false);
    this.submitted.set(false);
  }
  dirty(): boolean {
    return JSON.stringify(this.draft) !== this.original;
  }
  changed(): void {
    this.errors = {};
    this.message.set('');
  }
  blur(field: string): void {
    this.touched.add(field);
  }
  error(field: string): string {
    return (
      this.errors[field] ??
      (this.submitted() || this.touched.has(field)
        ? (draftIntent(this.draft).errors[field] ?? '')
        : '')
    );
  }
  async reload(): Promise<void> {
    const plan = this.plan();
    if (!plan || this.saving()) return;
    this.saving.set(true);
    try {
      this.reset(await this.api.detail(plan.id));
    } catch (error) {
      this.message.set((error as Error).message);
    } finally {
      this.saving.set(false);
    }
  }
  async save(): Promise<void> {
    if (this.saving()) return;
    this.submitted.set(true);
    this.message.set('');
    const parsed = draftIntent(this.draft);
    if (Object.keys(parsed.errors).length) {
      this.entry()?.focusFirstField();
      return;
    }
    this.saving.set(true);
    try {
      const plan = this.plan();
      const input = plan
        ? { ...parsed.intent, id: plan.id, expectedRevision: this.revision }
        : { ...parsed.intent, state: this.draft.state as 'IDEA' | 'PLANNED' };
      const key = this.attempt.for(input);
      const payload = plan
        ? await this.api.command(
            UpdatePlantingPlanDocument,
            { input: input as UpdatePlantingPlanInput, clientMutationId: key },
            (data) => data.updatePlantingPlan,
          )
        : await this.api.command(
            CreatePlantingPlanDocument,
            { input, clientMutationId: key },
            (data) => data.createPlantingPlan,
          );
      if (payload.problems.length) {
        this.errors = problemMap(payload.problems);
        this.conflict.set(payload.problems.some((p) => p.code === 'CONFLICT'));
        this.message.set(payload.problems.map((p) => p.message).join(' '));
        return;
      }
      if (!payload.plan)
        throw new Error('The server did not return the saved plan. Retry with the same values.');
      this.entry()?.markPristine();
      this.saved.emit(payload.plan);
    } catch (error) {
      this.message.set((error as Error).message);
    } finally {
      this.saving.set(false);
    }
  }
  async cancelPlan(): Promise<void> {
    const plan = this.plan();
    if (plan && (await this.entry()?.canLeave())) this.cancelRequested.emit(plan);
  }
}
