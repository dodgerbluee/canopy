import { ChangeDetectionStrategy, Component, effect, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { filter } from 'rxjs';
import { SessionService } from '../core/auth/session';
import { UiIcon } from '../ui/icon';
import { UiMenu, UiMenuItem } from '../ui/menu';
import { UiTheme } from '../ui/theme';
import { UiToastRegion } from '../ui/toast';

/** Human route names for the topbar breadcrumb. Never a UUID or session value. */
const ROUTE_LABELS: Record<string, string> = {
  '': 'Today',
  plants: 'Plants',
  'plants/new': 'New plant',
  journal: 'Journal',
  calendar: 'Calendar',
  'calendar/year': 'Garden year',
  year: 'Garden year',
  insights: 'AI assistant',
  more: 'More',
  rounds: 'Garden rounds',
  care: 'Care',
  inbox: 'Inbox',
  purchases: 'Purchases',
  'purchases/new': 'New purchase',
  settings: 'Settings',
};

/**
 * Pure URL → label mapping for the routes in app.routes.ts. Detail and form
 * screens name themselves instead of showing a bare id, and anything unknown
 * falls back to the section it sits under.
 */
export function routeLabelFor(url: string): string {
  const path = (url.split('?')[0] ?? '').split('#')[0].replace(/^\/+|\/+$/g, '');
  const exact = ROUTE_LABELS[path];
  if (exact) return exact;
  const [section, ...rest] = path.split('/');
  switch (section) {
    case 'plants':
      if (rest[0] === 'new') return 'New plant';
      if (rest[1] === 'edit') return 'Edit plant';
      if (rest[1] === 'compare') return 'Compare photos';
      return 'Plant detail';
    case 'purchases':
      return rest[0] === 'new' ? 'New purchase' : 'Edit purchase';
    case 'care':
      return rest[0] === 'plan' ? 'Care plan' : 'Care';
    default:
      return ROUTE_LABELS[section] ?? 'Today';
  }
}

/**
 * Official UI01 shell: forest sidebar + warm canvas over the guarded routes,
 * with a human breadcrumb and account menu. Every shipped route
 * stays a real anchor for keyboard and deep links; on mobile the bottom nav
 * carries the primary routes and a dedicated More page carries the rest.
 */
@Component({
  selector: 'app-shell',
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './shell.html',
  styleUrl: './shell.css',
  imports: [RouterOutlet, RouterLink, RouterLinkActive, UiIcon, UiMenu, UiMenuItem, UiToastRegion],
})
export class AppShell {
  private readonly session = inject(SessionService);
  private readonly router = inject(Router);

  protected readonly routeLabel = signal(routeLabelFor(this.router.url));

  constructor() {
    inject(UiTheme);
    this.router.events
      .pipe(
        filter((ev): ev is NavigationEnd => ev instanceof NavigationEnd),
        takeUntilDestroyed(),
      )
      .subscribe((ev) => this.routeLabel.set(routeLabelFor(ev.urlAfterRedirects)));

    // A 401 from any GraphQL call (session.markExpired) flips state to 'anon'
    // with ended 'expired', but nothing navigates: a busy/dirty modal route
    // would just sit there. Mirror authGuard's target here so the shell leaves
    // the guarded area; the entry guard already lets signed-out visitors
    // through, so the modal disposes without a prompt. Initial unknown/anon
    // and normal logout (ended 'logout', which navigates itself) are ignored.
    effect(() => {
      if (this.session.state() === 'anon' && this.session.ended() === 'expired') {
        void this.router.navigate(['/login'], {
          queryParams: { returnUrl: this.router.url },
          fragment: 'expired',
        });
      }
    });
  }

  protected async signOut(): Promise<void> {
    await this.session.logout();
    await this.router.navigateByUrl('/login');
  }
}
