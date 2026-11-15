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
import { AskPlantingPlannerDocument, type AskPlantingPlannerMutation } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon } from '../../ui/icon';
import { resultData } from '../calendar/calendar-data';
import { PlanningApi, type Plan, type Detail } from './planning-data';
type Reply = AskPlantingPlannerMutation['askPlantingPlanner'];
export type ReviewedPlanningNote = { plan: Detail; text: string; expectedRevision: number };
@Component({
  selector: 'planning-assistant',
  imports: [FormsModule, UiEntryDialog, UiIcon],
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrls: ['./planning-dialogs.css', './planning-assistant.css'],
  templateUrl: './planning-assistant.html',
})
export class PlanningAssistant {
  private readonly gql = inject(GraphQLClient);
  private readonly api = inject(PlanningApi);
  readonly open = input(false);
  readonly plans = input<Plan[]>([]);
  readonly year = input(new Date().getFullYear());
  readonly dismissed = output<void>();
  readonly reviewed = output<ReviewedPlanningNote>();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly selected = signal(new Set<string>());
  readonly question = signal('');
  readonly asking = signal(false);
  readonly reviewing = signal(false);
  readonly message = signal('');
  readonly reply = signal<Reply | null>(null);
  private original = '';
  private abort?: AbortController;
  private generation = 0;
  constructor() {
    let wasOpen = false;
    effect(() => {
      const open = this.open();
      untracked(() => {
        if (open && !wasOpen) {
          const ids = this.plans().map((p) => p.id);
          this.selected.set(new Set(ids.length <= 20 ? ids : []));
          this.question.set(
            'Help me choose the next step. Explain the supported windows and what is still unknown.',
          );
          this.original = this.signature();
          this.message.set('');
          this.reply.set(null);
          this.asking.set(false);
          this.reviewing.set(false);
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
  private signature(): string {
    return JSON.stringify([this.question(), [...this.selected()].sort()]);
  }
  dirty(): boolean {
    return this.signature() !== this.original;
  }
  toggle(id: string): void {
    this.selected.update((ids) => {
      const next = new Set(ids);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
    this.reply.set(null);
  }
  async ask(): Promise<void> {
    if (this.asking()) return;
    this.message.set('');
    const ids = [...this.selected()];
    if (ids.length < 1 || ids.length > 20 || this.question().trim().length < 3) {
      this.message.set('Choose 1–20 plans and enter a question.');
      return;
    }
    const n = ++this.generation;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.asking.set(true);
    this.reply.set(null);
    try {
      const reply = resultData(
        await this.gql.mutate(
          AskPlantingPlannerDocument,
          { question: this.question().trim(), planIds: ids, year: this.year() },
          abort.signal,
        ),
      ).askPlantingPlanner;
      if (n === this.generation) this.reply.set(reply);
    } catch (e) {
      if (n === this.generation) this.message.set((e as Error).message);
    } finally {
      if (n === this.generation) this.asking.set(false);
    }
  }
  async reviewNote(): Promise<void> {
    const result = this.reply(),
      note = result?.note;
    if (!result || !note || this.reviewing()) return;
    const n = ++this.generation;
    this.abort?.abort();
    const abort = (this.abort = new AbortController());
    this.reviewing.set(true);
    this.message.set('');
    try {
      const plan = await this.api.detail(note.planId, abort.signal);
      if (n !== this.generation) return;
      if (plan.revision !== note.expectedRevision)
        throw new Error(
          'This plan changed after the answer. Ask again before applying this draft.',
        );
      const urls = [
        ...new Set(
          result.sources.filter((s) => s.planId === note.planId).map((s) => s.fact.sourceUrl),
        ),
      ];
      const text = note.text + (urls.length ? '\n\nSources:\n' + urls.join('\n') : '');
      this.entry()?.markPristine();
      this.reviewed.emit({ plan, text, expectedRevision: note.expectedRevision });
    } catch (e) {
      if (n === this.generation) this.message.set((e as Error).message);
    } finally {
      if (n === this.generation) this.reviewing.set(false);
    }
  }
}
