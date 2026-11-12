import { ChangeDetectionStrategy, Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { UiIcon, type UiIconName } from '../../ui/icon';

@Component({
  selector: 'garden-more',
  imports: [RouterLink, UiIcon],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `<header class="page-head">
      <p class="eyebrow">YOUR GARDEN TOOLKIT</p>
      <h1 class="editorial">A place for everything.</h1>
      <p class="caption">Plan ahead, look back, or make Canopy your own.</p>
    </header>
    <nav class="more-grid" aria-label="More destinations">
      @for (item of items; track item.path) {
        <a class="card more-link" [routerLink]="item.path"
          ><span class="more-icon"><ui-icon [name]="item.icon" [size]="24" /></span
          ><span
            ><strong>{{ item.label }}</strong
            ><small>{{ item.description }}</small></span
          ><ui-icon name="arrow-up-right" [size]="17"
        /></a>
      }
    </nav>`,
  styles: `
    :host {
      display: block;
      max-width: 900px;
    }
    .page-head {
      margin-bottom: 28px;
    }
    .more-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 14px;
    }
    .more-link {
      display: flex;
      align-items: center;
      gap: 16px;
      text-decoration: none;
      color: var(--text-primary);
      padding: 22px;
      min-width: 0;
    }
    .more-link > span:nth-child(2) {
      flex: 1;
      min-width: 0;
    }
    .more-icon {
      color: var(--accent);
      background: var(--surface-raised);
      padding: 12px;
      border-radius: 14px;
    }
    .more-link strong {
      display: block;
      font-size: 15px;
    }
    .more-link small {
      display: block;
      color: var(--text-muted);
      line-height: 1.6;
      margin-top: 5px;
    }
    .more-link:hover {
      border-color: var(--accent);
    }
    @media (max-width: 600px) {
      .more-grid {
        grid-template-columns: 1fr;
      }
      .more-link {
        padding: 18px;
      }
    }
  `,
})
export class MorePage {
  readonly items: { path: string; label: string; description: string; icon: UiIconName }[] = [
    {
      path: '/insights',
      label: 'AI assistant',
      description: 'Ask about your garden and review plant photos.',
      icon: 'sparkles',
    },
    {
      path: '/care',
      label: 'Care',
      description: 'Scheduled work and seasonal guidance to consider.',
      icon: 'check',
    },
    {
      path: '/calendar',
      label: 'Calendar',
      description: 'Photos, watering and rain on the day they happened.',
      icon: 'calendar',
    },
    {
      path: '/journal',
      label: 'Journal',
      description: 'The story of each plant and your whole garden.',
      icon: 'book',
    },
    {
      path: '/inbox',
      label: 'Inbox',
      description: 'Reminders and suggestions that need your attention.',
      icon: 'inbox',
    },
    {
      path: '/purchases',
      label: 'Purchases',
      description: 'Receipts, acquisitions and garden spending.',
      icon: 'receipt',
    },
    {
      path: '/settings',
      label: 'Settings',
      description: 'Appearance, garden location and AI connections.',
      icon: 'settings',
    },
  ];
}
