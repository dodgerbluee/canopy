import { Component, input } from '@angular/core';

/** Status is always icon + text label — never color alone. */
@Component({
  selector: 'ui-status',
  template: `
    <span class="status" [class.status-ok]="kind() === 'ok'" [class.status-warn]="kind() === 'warn'" [class.status-danger]="kind() === 'danger'">
      @switch (kind()) {
        @case ('ok') {
          <svg viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="M8 16A8 8 0 1 1 8 0a8 8 0 0 1 0 16Zm3.7-9.6-4.2 4.2a1 1 0 0 1-1.4 0L4.3 8.5a1 1 0 1 1 1.4-1.4l1.1 1 3.5-3.4a1 1 0 0 1 1.4 1.4Z"/></svg>
        }
        @case ('warn') {
          <svg viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="M8 1.5 15 14H1L8 1.5Zm0 4v4m0 2v1" stroke="currentColor" stroke-width="1.6" fill="none" stroke-linecap="round"/></svg>
        }
        @case ('danger') {
          <svg viewBox="0 0 16 16" aria-hidden="true"><path fill="currentColor" d="M8 16A8 8 0 1 1 8 0a8 8 0 0 1 0 16ZM5.7 5.7a1 1 0 0 1 1.4 0L8 6.6l.9-.9a1 1 0 1 1 1.4 1.4L8.9 8l.9.9a1 1 0 0 1-1.4 1.4L8 9.4l-.9.9a1 1 0 0 1-1.4-1.4L7.1 8l-.9-.9a1 1 0 0 1 0-1.4Z"/></svg>
        }
      }
      <span>{{ label() }}</span>
    </span>
  `,
})
export class UiStatus {
  readonly kind = input<'ok' | 'warn' | 'danger'>('ok');
  readonly label = input.required<string>();
}
