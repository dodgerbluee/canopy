import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { UiIcon } from '../../ui/icon';

/** Shared navigation for the observed month and advisory seasonal year. */
@Component({
  selector: 'calendar-view-switch',
  imports: [RouterLink, UiIcon],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <nav aria-label="Calendar view">
      <a routerLink="/calendar" [attr.aria-current]="active() === 'month' ? 'page' : null">
        <ui-icon name="calendar" [size]="16" />Month
      </a>
      <a routerLink="/year" [attr.aria-current]="active() === 'year' ? 'page' : null">
        <ui-icon name="sprout" [size]="16" />Year
      </a>
    </nav>
  `,
  styles: `
    :host {
      display: block;
      margin-bottom: 26px;
    }
    nav {
      display: inline-flex;
      gap: 4px;
      padding: 4px;
      border: 1px solid var(--border-decorative);
      border-radius: var(--radius-pill);
      background: var(--surface-base);
    }
    a {
      display: flex;
      gap: 7px;
      align-items: center;
      padding: 8px 16px;
      min-height: 36px;
      border-radius: var(--radius-pill);
      color: var(--text-muted);
      text-decoration: none;
      font-size: 12px;
    }
    a[aria-current] {
      background: var(--accent);
      color: var(--accent-fg);
    }
    @media (max-width: 600px) {
      :host {
        margin-bottom: 20px;
      }
    }
  `,
})
export class CalendarViewSwitch {
  readonly active = input.required<'month' | 'year'>();
}
