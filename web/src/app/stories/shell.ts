import { Component, DestroyRef, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { UiMenu, UiMenuItem } from '../ui/menu';
import { UiTheme } from '../ui/theme';
import { UiToastRegion, UiToasts } from '../ui/toast';

/** R1 release-aware navigation: Plants/Journal/Purchases/Settings; Today and
 * Care are R2 and shown disabled — never a blank placeholder. */
@Component({
  selector: 'stories-shell',
  imports: [RouterOutlet, RouterLink, RouterLinkActive, UiMenu, UiMenuItem, UiToastRegion],
  template: `
    <div class="shell">
      <nav class="shell-nav" aria-label="Primary">
        <div class="shell-brand">Canopy</div>
        <a class="nav-link is-disabled" aria-disabled="true" (click)="unavailable('Today')">
          <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.5"/></svg>
          <span class="nav-text">Today</span>
          <span class="badge" style="margin-left:auto">R2</span>
        </a>
        <a class="nav-link" routerLink="/stories/plants" routerLinkActive="on" [attr.aria-current]="null"><span class="nav-text">Plants</span></a>
        <a class="nav-link" routerLink="/stories/purchase" routerLinkActive="on"><span class="nav-text">Purchases</span></a>
        <a class="nav-link" routerLink="/stories/credits" routerLinkActive="on"><span class="nav-text">Settings</span></a>
        <a class="nav-link is-disabled" aria-disabled="true" (click)="unavailable('Care')"><span class="nav-text">Care</span><span class="badge" style="margin-left:auto">R2</span></a>
        <hr class="divider" />
        <h4 class="caption" style="margin:0 0 var(--sp-1)">Visual fixtures</h4>
        <a class="nav-link" routerLink="/stories/today"><span class="nav-text">Today screen</span></a>
        <a class="nav-link" routerLink="/stories/plant"><span class="nav-text">Plant detail</span></a>
        <a class="nav-link" routerLink="/stories/capture"><span class="nav-text">Capture</span></a>
        <a class="nav-link" routerLink="/stories/compare"><span class="nav-text">Compare</span></a>
        <a class="nav-link" routerLink="/stories/insight"><span class="nav-text">Insight</span></a>
      </nav>
      <main class="shell-main">
        <header class="topbar">
          <div class="row">
            <span class="badge">
              <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16"><path d="M8 1.5 15 14H1Z" fill="none" stroke="currentColor" stroke-width="1.5"/></svg>
              Home · Sun 1 Nov
            </span>
          </div>
          <div class="row">
            <ui-menu label="Add">
              <button uiMenuItem (triggered)="toast('New plant form opened (fixture)')">New plant</button>
              <button uiMenuItem (triggered)="toast('Photo capture opened (fixture)')">Add photos</button>
              <button uiMenuItem (triggered)="toast('Quick log opened (fixture)')">Quick log</button>
            </ui-menu>
            <button type="button" class="btn" (click)="toggleTheme()">
              Theme: {{ theme.theme() }}
            </button>
          </div>
        </header>
        <div class="page">
          <router-outlet />
        </div>
      </main>
    </div>
    <button type="button" class="fab" aria-label="Add" (click)="toast('Quick add (fixture)')">+</button>
    <nav class="bottom-nav" aria-label="Primary mobile">
      <a class="nav-link" routerLink="/stories/today"><span>Today</span></a>
      <a class="nav-link" routerLink="/stories/plants"><span>Plants</span></a>
      <a class="nav-link" routerLink="/stories/plant"><span>Journal</span></a>
      <a class="nav-link" routerLink="/stories/today"><span>Care</span></a>
      <a class="nav-link" routerLink="/stories/credits"><span>More</span></a>
    </nav>
    <ui-toast-region />
  `,
})
export class StoriesShell {
  protected readonly theme = inject(UiTheme);
  private readonly router = inject(Router);
  private readonly toasts = inject(UiToasts);

  constructor() {
    // deterministic theming for visual tests: /stories/<x>?theme=dark|light
    inject(ActivatedRoute).queryParamMap
      .pipe(takeUntilDestroyed(inject(DestroyRef)))
      .subscribe((qp) => {
        const t = qp.get('theme');
        if (t === 'light' || t === 'dark') this.theme.set(t);
      });
  }

  protected toggleTheme(): void {
    this.theme.set(this.theme.theme() === 'dark' ? 'light' : 'dark');
  }
  protected toast(msg: string): void {
    this.toasts.ok(msg);
  }
  protected unavailable(feature: string): void {
    this.toasts.ok(`${feature} arrives with the next release.`);
  }
}
