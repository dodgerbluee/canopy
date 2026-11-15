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
import { AddPlantingToGardenDocument, type PlantFieldsFragment } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { calendarPlants, workspaceDay } from '../calendar/calendar-data';
import { PlanningApi, PlanningAttempt, type Plan } from './planning-data';
import { gardenDraft, actualPlantInput } from './garden-draft';
import { isCivilDate } from './planning-form-data';
@Component({
  selector: 'planning-garden-editor',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './planning-dialogs.css',
  templateUrl: './garden-editor.html',
})
export class GardenEditor {
  private readonly api = inject(PlanningApi);
  private readonly gql = inject(GraphQLClient);
  readonly open = input(false);
  readonly plan = input<Plan | null>(null);
  readonly action = input<Plan['actions'][number] | null>(null);
  readonly timezone = input('UTC');
  readonly currency = input('USD');
  readonly locations = input<Awaited<ReturnType<PlanningApi['context']>>['locations']>([]);
  readonly dismissed = output<void>();
  readonly saved = output<Plan>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly saving = signal(false);
  readonly message = signal('');
  readonly loading = signal(false);
  readonly plants = signal<PlantFieldsFragment[]>([]);
  readonly today = signal('');
  draft = gardenDraft(null, 'USD');
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
          this.draft = gardenDraft(this.plan(), this.currency());
          if (this.action()) this.draft.completedOn = this.today();
          this.original = JSON.stringify(this.draft);
          this.attempt = new PlanningAttempt();
          this.message.set('');
          this.plants.set([]);
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
  async chooseMode(mode: string): Promise<void> {
    this.draft.mode = mode;
    if (mode !== 'existing' || this.plants().length) return;
    const n = ++this.generation;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.loading.set(true);
    this.message.set('');
    try {
      const plants = await calendarPlants(this.gql, abort.signal);
      if (n === this.generation) this.plants.set(plants.filter((p) => p.status === 'ACTIVE'));
    } catch {
      if (n === this.generation)
        this.message.set('Existing plants could not be loaded. Retry choosing an existing plant.');
    } finally {
      if (n === this.generation) this.loading.set(false);
    }
  }
  async save(): Promise<void> {
    const p = this.plan();
    if (!p || this.saving()) return;
    this.message.set('');
    try {
      const action = this.action();
      const on = action ? this.draft.completedOn : null;
      if (on && (!isCivilDate(on) || on > this.today()))
        throw new Error('Choose the real sowing or planting day, on or before today.');
      if (action && !on) throw new Error('Choose the actual sowing or planting day.');
      const existing =
        this.draft.mode === 'existing'
          ? this.plants().find((p) => p.id === this.draft.existingId)
          : null;
      if (this.draft.mode === 'existing' && !existing)
        throw new Error('Choose one active plant from your garden.');
      const input = {
        planId: p.id,
        expectedRevision: p.revision,
        newPlant: existing ? null : actualPlantInput(this.draft, this.today(), on),
        existingPlantId: existing?.id ?? null,
        actionId: action?.id ?? null,
        completedOn: on,
        expectedPlantRevision: existing && action ? existing.revision : null,
      };
      this.saving.set(true);
      const payload = await this.api.command(
        AddPlantingToGardenDocument,
        { input, clientMutationId: this.attempt.for(input) },
        (d) => d.addPlantingToGarden,
      );
      if (payload.problems.length) {
        this.message.set(
          payload.problems.map((p) => p.message).join(' ') +
            ' Reload the plan if it changed elsewhere.',
        );
        return;
      }
      if (!payload.plan)
        throw new Error('The save could not be confirmed. Retry these same values safely.');
      this.entry()?.markPristine();
      this.saved.emit(payload.plan);
    } catch (e) {
      this.message.set((e as Error).message);
    } finally {
      this.saving.set(false);
    }
  }
}
