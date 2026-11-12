import { Component, EventEmitter, Output, input } from '@angular/core';
import { STORY_STATES, StoryState, photoCredits } from '../../fixtures/collection';

/** State selector for visual fixtures: every screen renders the full state
 * contract (loading/empty/error/offline/conflict/session) from one layout. */
@Component({
  selector: 'story-bar',
  template: `
    <div class="row" role="group" aria-label="Fixture state">
      @for (s of states; track s) {
        <button
          type="button"
          class="chip"
          [attr.aria-pressed]="s === current()"
          (click)="set.emit(s)"
        >
          {{ s }}
        </button>
      }
    </div>
  `,
})
export class StoryBar {
  readonly current = input.required<StoryState>();
  @Output() readonly set = new EventEmitter<StoryState>();
  protected readonly states = STORY_STATES;
}

@Component({
  selector: 'story-error',
  template: `
    <div class="card" role="alert">
      <div class="row spread">
        <h2 style="margin:0">{{ title() }}</h2>
        <button type="button" class="btn btn-primary" (click)="retry.emit()">Retry</button>
      </div>
      <p class="small" style="margin-top:var(--sp-2)">{{ message() }}</p>
      <p class="caption mono">ref: {{ corr() }}</p>
    </div>
  `,
})
export class StoryError {
  readonly title = input('Something went wrong');
  readonly message = input('We could not load this data. Your data is safe.');
  readonly corr = input('req_9f2c41');
  @Output() readonly retry = new EventEmitter<void>();
}

@Component({
  selector: 'story-empty',
  template: `
    <div class="card" style="text-align:center; padding: var(--sp-12) var(--sp-6)">
      <h2>{{ title() }}</h2>
      <p class="small" style="color:var(--text-muted)">{{ hint() }}</p>
      <button type="button" class="btn btn-primary" (click)="add.emit()">{{ action() }}</button>
    </div>
  `,
})
export class StoryEmpty {
  readonly title = input('Nothing here yet');
  readonly hint = input('');
  readonly action = input('Add your first plant');
  @Output() readonly add = new EventEmitter<void>();
}

@Component({
  selector: 'story-session',
  template: `
    <div class="card" role="status">
      <div class="row spread">
        <div>
          <h2 style="margin:0">Session expired</h2>
          <p class="small" style="margin-top:var(--sp-1); color:var(--text-muted)">
            Your unsaved draft is kept in memory until you sign in again.
            Receipts and photos are never stored in this browser.
          </p>
        </div>
        <button type="button" class="btn btn-primary">Sign in again</button>
      </div>
    </div>
  `,
})
export class StorySession {}

@Component({
  selector: 'story-credits',
  template: `<p class="caption">{{ text }}</p>`,
})
export class StoryCredits {
  protected readonly text = photoCredits;
}
