import { requestId } from '../../core/request-id';
import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { DatePipe } from '@angular/common';
import { RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import {
  AcceptSuggestionDocument,
  DismissSuggestionDocument,
  type AnalysisRunFieldsFragment,
  type SuggestionFieldsFragment,
} from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon } from '../../ui/icon';
import { readableSuggestionText } from './suggestion-text';
import { UiToasts } from '../../ui/toast';

/**
 * One PROPOSED suggestion, shown as the contract intends: what was
 * observed, the qualified hypothesis, the suggested check, and the
 * evidence trail. Accepting previews exactly what it does — one one-off
 * check task in the plant's care plan, due a date the owner picks. The
 * REVIEW_REQUIRED touch (source/context moved since the advice was
 * written) is surfaced as a review-then-confirm step, never auto-
 * retried; CONFLICT/EXPIRED/NOT_FOUND show the server's own message and
 * re-read instead of guessing.
 */
@Component({
  selector: 'insight-suggestion-card',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [DatePipe, RouterLink, FormsModule, UiEntryDialog, UiIcon],
  styleUrl: './suggestion-card.css',
  template: `
    <article class="suggestion" [attr.aria-label]="kindLabel() + ' suggestion'">
      <div class="suggestion-heading">
        <span class="suggestion-icon"
          ><ui-icon [name]="s().kind === 'COST' ? 'receipt' : 'leaf'" [size]="18"
        /></span>
        <div class="suggestion-title">
          <p class="suggestion-kind">
            {{ kindLabel() }}
            @if (s().needsReview) {
              <span class="review-badge">Review needed</span>
            }
          </p>
          <h3>{{ s().kind === 'COST' ? 'Review recorded spending' : readable(s().action) }}</h3>
        </div>
      </div>
      <div class="suggestion-bottom">
        <details class="suggestion-details">
          <summary aria-label="Why this suggestion">
            <span class="why-wide">Why this suggestion</span><span class="why-short">Why</span>
            <ui-icon name="chevrons" [size]="14" />
          </summary>
          <div class="suggestion-context">
            @if (s().kind === 'COST') {
              <p>{{ readable(s().action) }}</p>
            }
            @if (s().observation) {
              <section>
                <h4>Recorded context</h4>
                <p>{{ readable(s().observation) }}</p>
              </section>
            }
            @if (s().hypothesis) {
              <section>
                <h4>Possible explanation</h4>
                <p>{{ readable(s().hypothesis) }}</p>
                <span class="context-note">A possibility to check, not a diagnosis.</span>
              </section>
            }
            <section>
              <h4>Supporting records</h4>
              @if (evidenceGroups().length) {
                <div class="evidence-summary">
                  @for (group of evidenceGroups(); track group.kind) {
                    @if (group.path) {
                      <a [routerLink]="group.path" [queryParams]="group.qs">{{ group.label }}</a>
                    } @else {
                      <span>{{ group.label }}</span>
                    }
                  }
                </div>
              } @else {
                <p>No evidence records were attached — treat it as unsupported.</p>
              }
            </section>
            @if (s().missingFacts.length) {
              <section>
                <h4>What’s still unknown</h4>
                <ul>
                  @for (fact of s().missingFacts; track $index) {
                    <li>{{ readable(fact) }}</li>
                  }
                </ul>
              </section>
            }
            @if (s().expiresAt) {
              <p class="context-note">Available until {{ s().expiresAt | date: 'mediumDate' }}.</p>
            }
          </div>
        </details>
        <div class="suggestion-actions">
          <button
            type="button"
            class="btn add-task"
            (click)="openAccept()"
            [disabled]="busy() || dismissing()"
          >
            <ui-icon name="plus" [size]="15" />Add to care
          </button>
          <button
            type="button"
            class="dismiss-action"
            (click)="openDismiss()"
            [disabled]="busy() || accepting()"
          >
            Dismiss
          </button>
        </div>
      </div>
      @if (notice() && !accepting() && !dismissing()) {
        <p role="status" class="suggestion-notice">{{ notice() }}</p>
      }
    </article>

    <!-- Accept: the due date and the confirm step live in the shared modal.
         A REVIEW_REQUIRED response keeps the modal open and shows the actual
         updated review warning, proposal and current revision inside it, so
         the second explicit Confirm accept is taken with that context. -->
    <ui-entry-dialog
      #acceptEntry
      [open]="accepting()"
      title="Add to care"
      description="Schedule a check in your care plan. You choose when it’s due."
      size="regular"
      [busy]="busy()"
      [dirty]="acceptDirty()"
      (dismissed)="onAcceptDismissed()"
    >
      @if (accepting()) {
        <div class="entry-form">
          @if (confirmRev() !== null) {
            <div class="form-section" role="status">
              <h3>Review required before accepting</h3>
              <p class="field-hint">{{ reviewNotice() }}</p>
              <p class="field-hint">
                Current revision: {{ confirmRev() }}. The proposal below is the one that will be
                accepted at that revision.
              </p>
              <p class="field-hint">{{ notice() }}</p>
            </div>
          }
          <div class="form-section">
            <h3>Proposal</h3>
            <p class="field-hint">{{ readable(s().action) }}</p>
            @if (s().observation) {
              <p class="field-hint"><strong>Observed:</strong> {{ readable(s().observation) }}</p>
            }
            @if (s().hypothesis) {
              <p class="field-hint">
                <strong>Hypothesis (not a diagnosis):</strong>
                {{ readable(s().hypothesis) }}
              </p>
            }
            @if (s().missingFacts.length) {
              <p class="field-hint">
                <strong>Missing context:</strong>
                {{ readable(s().missingFacts.join('; ')) }}
              </p>
            }
          </div>
          <div class="form-section">
            <div
              class="row wrap"
              role="group"
              aria-label="Evidence behind this suggestion"
              style="gap: var(--sp-1)"
            >
              @for (group of evidenceGroups(); track group.kind) {
                @if (group.path) {
                  <a class="chip" [routerLink]="group.path" [queryParams]="group.qs">{{
                    group.label
                  }}</a>
                } @else {
                  <span class="chip">{{ group.label }}</span>
                }
              }
              @if (!evidenceGroups().length) {
                <span class="caption">No evidence records attached — treat as unsupported.</span>
              }
            </div>
          </div>
          <div class="form-section">
            <label class="field-label" for="sg-due">Due</label>
            <input
              id="sg-due"
              class="input"
              type="date"
              name="dueOn"
              [attr.min]="today"
              [(ngModel)]="dueOn"
              [disabled]="busy()"
            />
            <p class="field-hint">The date the check task is due in the care plan.</p>
            @if (notice()) {
              <p role="status" class="field-error">{{ notice() }}</p>
            }
          </div>
          <div class="form-actions">
            <button type="button" class="btn" (click)="closeAccept()">Cancel</button>
            <button type="button" class="btn btn-primary" [disabled]="busy()" (click)="accept()">
              {{ confirmRev() !== null ? 'Confirm accept' : 'Add check' }}
            </button>
          </div>
        </div>
      }
    </ui-entry-dialog>

    <!-- Dismiss: explicit compact confirmation on the same mutation; the
         confirmation stays mounted while the mutation is in flight. -->
    <ui-entry-dialog
      #dismissEntry
      [open]="dismissing()"
      title="Dismiss suggestion"
      description="Dismissal is final — the proposal is kept on the record"
      size="compact"
      [busy]="busy()"
      (dismissed)="onDismissDismissed()"
    >
      @if (dismissing()) {
        <div class="entry-form">
          <div class="form-section">
            <p class="field-hint">{{ readable(s().action) }}</p>
            @if (notice()) {
              <p role="status" class="field-error">{{ notice() }}</p>
            }
          </div>
          <div class="form-actions">
            <button type="button" class="btn" (click)="closeDismiss()">Cancel</button>
            <button type="button" class="btn btn-danger" [disabled]="busy()" (click)="dismiss()">
              Dismiss
            </button>
          </div>
        </div>
      }
    </ui-entry-dialog>
  `,
})
export class InsightSuggestionCard implements OnDestroy {
  readonly suggestion = input.required<SuggestionFieldsFragment>();
  /** The run's evidence block, when the caller has it (plant insights tab):
   * lets chips name each id and link a two-photo pair to the compare view. */
  readonly evidence = input<AnalysisRunFieldsFragment['evidence'] | null>(null);
  /** Today-page cards link out to the plant's insights tab. */
  readonly plantLink = input(false);
  // payload: a conflict message to surface where the list lives ('' = plain
  // refresh) — the card itself may vanish on re-read, so its notice can't
  // be the only place the explanation appears
  readonly changed = output<string>();

  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  private readonly abort = new AbortController();

  protected readonly today = new Date().toISOString().slice(0, 10);
  protected readonly busy = signal(false);
  protected readonly notice = signal('');
  /** set after a REVIEW_REQUIRED touch: the next accept confirms at this revision */
  protected readonly confirmRev = signal<number | null>(null);
  /** the server's own REVIEW_REQUIRED wording, shown inside the open modal */
  protected readonly reviewNotice = signal('');
  protected dueOn = this.today;

  /** UI02: the entry (due date, confirm step, dismissal) lives in the shared
   * modal; the card itself stays a read-only record. */
  protected readonly accepting = signal(false);
  protected readonly dismissing = signal(false);
  /** the due date as it stood when the modal opened — closing with a different
   * date goes through the shared discard confirmation first */
  private dueBaseline = this.dueOn;

  private readonly acceptEntry = viewChild<UiEntryDialog>('acceptEntry');
  private readonly dismissEntry = viewChild<UiEntryDialog>('dismissEntry');

  /** template shorthand: the card and its modals read the same input */
  protected s = this.suggestion;

  protected acceptDirty(): boolean {
    // the date input is the only draft field; the shared dialog's automatic
    // tracking sees it, and this keeps the binding honest about it
    return this.dueOn !== this.dueBaseline;
  }

  protected openAccept(): void {
    if (this.busy() || this.dismissing()) return;
    this.notice.set('');
    this.dueBaseline = this.dueOn;
    this.accepting.set(true);
  }

  protected openDismiss(): void {
    if (this.busy() || this.accepting()) return;
    this.notice.set('');
    this.dismissing.set(true);
  }

  /** Cancel routes through the shared handle, so an edited due date gets the
   * same confirmation as an X — a cancelled review never mutates anything. */
  protected closeAccept(): void {
    this.acceptEntry()?.requestClose();
  }

  protected closeDismiss(): void {
    this.dismissEntry()?.requestClose();
  }

  protected onAcceptDismissed(): void {
    this.accepting.set(false);
    // a cancelled review leaves the proposal untouched at its own revision
    this.confirmRev.set(null);
    this.reviewNotice.set('');
    this.notice.set('');
  }

  protected onDismissDismissed(): void {
    this.dismissing.set(false);
    this.notice.set('');
  }

  ngOnDestroy(): void {
    this.abort.abort();
  }

  protected readonly readable = readableSuggestionText;

  protected kindLabel(): string {
    return this.suggestion().kind === 'COST' ? 'Spending review' : 'Care check';
  }

  protected evidenceGroups(): {
    kind: string;
    label: string;
    path: string[] | null;
    qs: Record<string, string> | null;
  }[] {
    const ev = this.evidence();
    const counts = new Map<string, number>();
    for (const id of new Set(this.s().evidenceIds)) {
      const kind = this.classify(id, ev);
      counts.set(kind, (counts.get(kind) ?? 0) + 1);
    }
    return [...counts].map(([kind, count]) => {
      const pair = kind === 'photo' && ev?.photoIds.length === 2 ? ev.photoIds : null;
      const plural = kind === 'activity' ? 'activities' : kind + 's';
      const qs: Record<string, string> | null = pair
        ? { a: pair[0], b: pair[1] }
        : this.plantLink()
          ? { tab: 'insights' }
          : null;
      return {
        kind,
        label: `${count} ${count === 1 ? kind : plural}${pair ? ' · Compare photos' : ''}`,
        path: pair
          ? ['/plants', this.s().plantId, 'compare']
          : this.plantLink()
            ? ['/plants', this.s().plantId]
            : null,
        qs,
      };
    });
  }

  private classify(id: string, ev: AnalysisRunFieldsFragment['evidence'] | null): string {
    if (!ev) return 'record';
    if (ev.photoIds.includes(id)) return 'photo';
    if (ev.activityIds.includes(id)) return 'activity';
    if (ev.measurementIds.includes(id)) return 'measurement';
    if (ev.sourceVersionIds.includes(id)) return 'source';
    if (ev.weatherSnapshotIds.includes(id)) return 'weather snapshot';
    return 'record';
  }

  protected async accept(): Promise<void> {
    const s = this.suggestion();
    const rev = this.confirmRev() ?? s.revision;
    if (this.busy()) return;
    this.busy.set(true);
    this.notice.set('');
    const r = await this.gql.mutate(
      AcceptSuggestionDocument,
      {
        input: {
          suggestionId: s.id,
          expectedRevision: rev,
          ...(this.dueOn ? { dueOn: this.dueOn } : {}),
        },
        clientMutationId: `${requestId()}:accept-suggestion`,
      },
      this.abort.signal,
    );
    this.busy.set(false);
    if (r.kind !== 'ok') {
      // the modal stays open with the date kept, so the retry is the same step
      this.notice.set(
        r.kind === 'auth' ? 'Session expired — sign in again.' : 'Could not reach the server.',
      );
      return;
    }
    const p = r.data?.acceptSuggestion;
    if (!p) {
      this.notice.set('The server did not answer that request.');
      return;
    }
    const prob = p.problems?.[0];
    if (!prob) {
      const due = p.occurrence?.currentDueDate ?? this.dueOn;
      this.dueBaseline = this.dueOn;
      // accepted: clear the draft state, then close
      this.acceptEntry()?.markPristine();
      this.accepting.set(false);
      this.toasts.ok(`Task added to care — due ${due}. It appears in the agenda.`);
      this.changed.emit('');
      return;
    }
    if (prob.code === 'VALIDATION' && prob.message.startsWith('REVIEW_REQUIRED')) {
      // first touch: stay open and show the actual updated review context —
      // the warning, the proposal being accepted and the current revision —
      // before the second explicit Confirm accept
      this.confirmRev.set(prob.currentRevision ?? null);
      this.reviewNotice.set(prob.message);
      this.notice.set(
        'Your review was recorded. Check the evidence above, then press Confirm accept.',
      );
      return;
    }
    this.notice.set(prob.message);
    this.changed.emit(prob.message);
  }

  protected async dismiss(): Promise<void> {
    const s = this.suggestion();
    if (this.busy()) return;
    this.busy.set(true);
    this.notice.set('');
    const r = await this.gql.mutate(
      DismissSuggestionDocument,
      {
        input: { suggestionId: s.id, expectedRevision: s.revision },
        clientMutationId: `${requestId()}:dismiss-suggestion`,
      },
      this.abort.signal,
    );
    this.busy.set(false);
    if (r.kind !== 'ok') {
      this.notice.set(
        r.kind === 'auth' ? 'Session expired — sign in again.' : 'Could not reach the server.',
      );
      return;
    }
    const p = r.data?.dismissSuggestion;
    const prob = p?.problems?.[0];
    if (!prob) {
      this.dismissEntry()?.markPristine();
      this.dismissing.set(false);
      this.toasts.ok('Suggestion dismissed — dismissal is final.');
      this.changed.emit('');
      return;
    }
    // conflict/expiry keeps the confirmation mounted with the server's own words
    this.notice.set(prob.message);
    this.changed.emit(prob.message);
  }
}
