import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { DatePipe, DecimalPipe } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import {
  DismissRainReviewDocument,
  RecordRainDocument,
  type TodayOverviewQuery,
} from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon } from '../../ui/icon';
import { UiToasts } from '../../ui/toast';
import { mutationKey, rainMillimeters, resultData } from '../calendar/calendar-data';

type Review = TodayOverviewQuery['todayOverview']['rainReview'];
type Proposal = Review['proposals'][number];

@Component({
  selector: 'today-rain',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [DatePipe, DecimalPipe, FormsModule, RouterLink, UiEntryDialog, UiIcon],
  templateUrl: './rain-panel.html',
  styleUrl: './rain-panel.css',
})
export class RainPanel {
  readonly review = input.required<Review>();
  readonly changed = output<void>();
  private readonly gql = inject(GraphQLClient);
  private readonly session = inject(SessionService);
  private readonly toasts = inject(UiToasts);
  private readonly dialog = viewChild<UiEntryDialog>('entry');
  private readonly keys = new Map<string, string>();
  private destroyed = false;
  readonly busy = signal(false);
  readonly error = signal('');
  readonly selected = signal<Proposal | null>(null);
  readonly amount = signal('');
  readonly unit = signal<'mm' | 'in'>('in');
  readonly hidden = signal(new Set<string>());
  readonly proposals = computed(() =>
    this.review().proposals.filter((p) => !this.hidden().has(p.id)),
  );

  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.destroyed = true;
    });
  }

  open(p: Proposal): void {
    this.amount.set('');
    this.unit.set('in');
    this.error.set('');
    this.selected.set(p);
  }
  cancel(): void {
    this.dialog()?.requestClose();
  }
  date(day: string): string {
    return new Date(day + 'T12:00:00').toLocaleDateString(undefined, {
      month: 'short',
      day: 'numeric',
    });
  }
  private key(input: unknown): string {
    const fingerprint = JSON.stringify([this.session.info()?.workspaceId, input]);
    let key = this.keys.get(fingerprint);
    if (!key) {
      key = mutationKey();
      this.keys.set(fingerprint, key);
    }
    return key;
  }
  async confirm(): Promise<void> {
    const p = this.selected();
    if (!p || this.busy()) return;
    this.error.set('');
    let amountMm: string | null;
    try {
      amountMm = rainMillimeters(this.amount(), this.unit());
    } catch (e) {
      this.error.set((e as Error).message);
      return;
    }
    const input = { day: p.day, amountMm, rainReviewId: p.id };
    const workspace = this.session.info()?.workspaceId;
    this.busy.set(true);
    try {
      const payload = resultData(
        await this.gql.mutate(RecordRainDocument, { input, clientMutationId: this.key(input) }),
      ).recordRain;
      if (this.destroyed || workspace !== this.session.info()?.workspaceId) return;
      if (payload.problems.length) throw new Error(payload.problems[0].message);
      if (!payload.rain)
        throw new Error('The save could not be confirmed. Retry with the same values.');
      this.dialog()?.markPristine();
      this.selected.set(null);
      this.hide(p.id);
      this.toasts.ok('Rain is on your calendar.');
      this.changed.emit();
    } catch (e) {
      if (!this.destroyed)
        this.error.set((e as Error).message + ' Your entries are still here; retrying is safe.');
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }
  async dismiss(p: Proposal): Promise<void> {
    if (this.busy()) return;
    this.error.set('');
    this.busy.set(true);
    const workspace = this.session.info()?.workspaceId;
    try {
      const payload = resultData(
        await this.gql.mutate(DismissRainReviewDocument, {
          input: { id: p.id },
          clientMutationId: this.key({ dismiss: p.id }),
        }),
      ).dismissRainReview;
      if (this.destroyed || workspace !== this.session.info()?.workspaceId) return;
      if (payload.problems.length) throw new Error(payload.problems[0].message);
      if (!payload.dismissedId)
        throw new Error('The response could not be confirmed. Please retry.');
      this.hide(p.id);
      this.changed.emit();
    } catch (e) {
      if (!this.destroyed) this.error.set((e as Error).message);
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }
  private hide(id: string): void {
    this.hidden.update((ids) => new Set([...ids, id]));
  }
}
