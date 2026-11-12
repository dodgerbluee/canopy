import { Component, inject, signal } from '@angular/core';
import { UiStatus } from '../ui/status';
import { UiToasts } from '../ui/toast';
import {
  StoryBar,
  StoryCredits,
  StoryError,
  StorySession,
} from './shared';
import { StoryState, insight } from '../../fixtures/collection';

/** Evidence sheet: what is visible, what changed, explanations, what is
 * missing, next check — with citations. Never a confident verdict. */
@Component({
  selector: 'story-insight',
  imports: [StoryBar, StoryError, StorySession, StoryCredits, UiStatus],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    @switch (state()) {
      @case ('error') { <story-error title="Insight unavailable" message="The analysis service did not answer. Nothing was changed on your plants." /> }
      @case ('offline') { <story-error title="You are offline" message="Evidence needs the stored photos to render." /> }
      @case ('session') { <story-session /> }
      @case ('empty') { <story-error title="No insights yet" message="Insights appear after a few dated observations — we never guess from a single photo." /> }
      @case ('conflict') { <story-error title="Already dismissed" message="This suggestion was dismissed on your phone. Reload to sync." /> }
      @default {
        <div class="row spread">
          <h1 style="margin:0">{{ insight.title }}</h1>
          <ui-status kind="warn" label="Proposed — needs your review" />
        </div>

        <div class="stack" style="margin-top:var(--sp-6); max-width: 720px">
          <section class="card">
            <h2>What is visible</h2>
            <p class="small">{{ insight.visible }}</p>
            <div class="grid-cards" style="grid-template-columns:1fr 1fr">
              @for (p of [insight.photoLeft, insight.photoRight]; track $index) {
                <img [src]="p" alt="Evidence photo supporting the observation"
                     style="width:100%; aspect-ratio:4/3; object-fit:cover; border-radius:var(--radius-field)" />
              }
            </div>
          </section>
          <section class="card">
            <h2>What changed</h2>
            <p class="small">{{ insight.changed }}</p>
          </section>
          <section class="card">
            <h2>Plausible explanations</h2>
            <ul class="small" style="margin:0; padding-left:var(--sp-4)">
              @for (e of insight.explanations; track e) { <li>{{ e }}</li> }
            </ul>
          </section>
          <section class="card">
            <h2>What we cannot see</h2>
            <ul class="small" style="margin:0; padding-left:var(--sp-4)">
              @for (m of insight.missing; track m) { <li>{{ m }}</li> }
            </ul>
          </section>
          <section class="card card-raised">
            <h2>Suggested next check</h2>
            <p class="small">{{ insight.nextCheck }}</p>
            <p class="caption">
              Source: {{ insight.citation.publisher }} · {{ insight.citation.date }} · {{ insight.citation.region }}
            </p>
            <div class="row" style="margin-top:var(--sp-3)">
              <button type="button" class="btn btn-primary" (click)="toasts.ok('Check added to your plan. Undo in activity log.')">Add to plan</button>
              <button type="button" class="btn" (click)="toasts.ok('Dismissed. We will not re-propose this pattern for 30 days.')">Dismiss</button>
            </div>
          </section>
        </div>
      }
    }
  `,
})
export class StoryInsight {
  protected readonly state = signal<StoryState>('ready');
  protected readonly insight = insight;
  protected readonly toasts = inject(UiToasts);
}
