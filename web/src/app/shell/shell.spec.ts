import { readFileSync } from 'node:fs';
import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Router, Routes, provideRouter } from '@angular/router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../core/auth/session';
import { UiTheme } from '../ui/theme';
import { AppShell, routeLabelFor } from './shell';

// jsdom has no matchMedia; UiTheme reads the system preference at construction.
function stubMatchMedia(matches = false): void {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}

@Component({ standalone: true, template: '' })
class BlankComponent {}

// The shell decides what is reachable, so the spec needs the routes to exist.
const routes: Routes = [
  { path: '', pathMatch: 'full', redirectTo: 'today' },
  { path: 'today', component: BlankComponent },
  { path: 'plants', component: BlankComponent },
  { path: 'rounds', component: BlankComponent },
  { path: 'calendar', component: BlankComponent },
  { path: 'year', component: BlankComponent },
  { path: 'more', component: BlankComponent },
  { path: 'insights', component: BlankComponent },
  { path: 'journal', component: BlankComponent },
  { path: 'care', component: BlankComponent },
  { path: 'inbox', component: BlankComponent },
  { path: 'purchases', component: BlankComponent },
  { path: 'settings', component: BlankComponent },
  { path: 'login', component: BlankComponent },
];

describe('AppShell', () => {
  let session: SessionService;
  let router: Router;

  beforeEach(() => {
    stubMatchMedia();
    TestBed.configureTestingModule({ providers: [provideRouter(routes)] });
    router = TestBed.inject(Router);
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'abcdef01-2345-6789-abcd-ef0123456789',
      expiresAt: '2026-11-02T00:00:00Z',
      csrfToken: 'c',
    });
  });

  function render() {
    const fixture = TestBed.createComponent(AppShell);
    fixture.detectChanges();
    return fixture;
  }

  it('renders desktop and mobile primary navigation with accessible names', () => {
    const root = render().nativeElement as HTMLElement;
    expect(root.querySelector('nav[aria-label="Primary"]')).toBeTruthy();
    expect(root.querySelector('nav[aria-label="Primary mobile"]')).toBeTruthy();
  });

  it('marks unshipped features disabled instead of routing to blanks', () => {
    const root = render().nativeElement as HTMLElement;
    const disabled = root.querySelectorAll('nav[aria-label="Primary"] a[aria-disabled="true"]');
    expect(disabled.length).toBe(0); // Journal shipped with T19 — nothing left unshipped in R1 nav
    const plants = [...root.querySelectorAll('nav[aria-label="Primary"] a')].find((a) =>
      a.textContent?.includes('Plants'),
    );
    expect(plants?.getAttribute('href')).toBe('/plants'); // shipped with T10
    const journal = [...root.querySelectorAll('nav[aria-label="Primary"] a')].find((a) =>
      a.textContent?.includes('Journal'),
    );
    expect(journal?.getAttribute('href')).toBe('/journal'); // shipped with T19
    const purchases = [...root.querySelectorAll('nav[aria-label="Primary"] a')].find((a) =>
      a.textContent?.includes('Purchases'),
    );
    expect(purchases?.getAttribute('href')).toBe('/purchases'); // shipped with T12
  });

  it('opens the plant collection from Your garden while Settings remains its own destination', () => {
    const root = render().nativeElement as HTMLElement;
    expect(root.querySelector('a.workspace')?.getAttribute('href')).toBe('/plants');
    const settings = [...root.querySelectorAll('nav[aria-label="Primary"] a')].find((a) =>
      a.textContent?.includes('Settings'),
    );
    expect(settings?.getAttribute('href')).toBe('/settings');
  });

  it('links Settings and Overview as real anchors for keyboard and deep links', () => {
    const root = render().nativeElement as HTMLElement;
    const settings = [...root.querySelectorAll('a')].find((a) =>
      a.textContent?.includes('Settings'),
    );
    expect(settings?.getAttribute('href')).toBe('/settings');
  });

  it('sign out clears the session and lands on /login', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const navigate = vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    const fixture = render();
    await fixture.componentInstance['signOut']();
    expect(session.state()).toBe('anon');
    expect(navigate).toHaveBeenCalledWith('/login');
    vi.unstubAllGlobals();
  });

  it('keeps desktop destinations reachable without promoting mobile rounds', () => {
    const root = render().nativeElement as HTMLElement;
    const hrefs = [...root.querySelectorAll('nav[aria-label="Primary"] a')].map((a) =>
      a.getAttribute('href'),
    );
    for (const path of [
      '/plants',
      '/calendar',
      '/journal',
      '/care',
      '/inbox',
      '/purchases',
      '/settings',
    ]) {
      expect(hrefs, `${path} is unreachable from the redesigned shell`).toContain(path);
    }
    expect(hrefs).not.toContain('/rounds');
    // Today is the shell root; the brand and the first nav item both point at it.
    expect(hrefs).toContain('/');
  });

  it('gives every nav item a visible accessible name, not only an icon', () => {
    const root = render().nativeElement as HTMLElement;
    const names = [...root.querySelectorAll('nav[aria-label="Primary"] a')].map((a) =>
      a.textContent?.replace(/\s+/g, ' ').trim(),
    );
    for (const name of names) expect(name).toBeTruthy();
    expect(names).toEqual([
      'Today',
      'Plants',
      'Garden year',
      'Calendar',
      'Journal',
      'Care',
      'AI assistant',
      'Inbox',
      'Purchases',
      'Settings',
    ]);
  });

  it('keeps mobile navigation short and the account menu free of navigation/theme duplicates', () => {
    const f = render(),
      root = f.nativeElement as HTMLElement;
    expect(
      [...root.querySelectorAll('nav[aria-label="Primary mobile"] a')].map((a) =>
        a.getAttribute('href'),
      ),
    ).toEqual(['/', '/plants', '/rounds', '/year', '/more']);
    (root.querySelector('ui-menu button') as HTMLButtonElement).click();
    f.detectChanges();
    expect(
      [...root.querySelectorAll('ui-menu [role="menuitem"]')].map((el) => el.textContent?.trim()),
    ).toEqual(['Sign out']);
    expect(root.querySelector('.topbar button[aria-label*="theme"]')).toBeNull();
  });

  it('names the current section in human words and never leaks the workspace id', async () => {
    const fixture = render();
    await router.navigate(['/journal']);
    fixture.detectChanges();
    const crumbs = (fixture.nativeElement as HTMLElement).querySelector('.crumbs')!;
    expect(crumbs.textContent).toContain('Journal');
    expect(crumbs.textContent).not.toContain('abcdef01');
    expect(crumbs.textContent).not.toContain('2345');
  });

  it('marks the current section current for assistive tech', async () => {
    const fixture = render();
    await router.navigate(['/care']);
    fixture.detectChanges();
    const current = (fixture.nativeElement as HTMLElement).querySelector(
      'nav[aria-label="Primary"] a[aria-current="page"]',
    );
    expect(current?.getAttribute('href')).toBe('/care');
  });

  it('applies the stored theme at startup without a header theme control', () => {
    const theme = TestBed.inject(UiTheme);
    theme.set('dark');
    render();
    expect(document.documentElement.dataset['theme']).toBe('dark');
  });

  it('keeps decorative glyphs out of the accessible name', () => {
    const root = render().nativeElement as HTMLElement;
    const svgs = [...root.querySelectorAll('ui-icon svg')];
    expect(svgs.length).toBeGreaterThan(0);
    for (const svg of svgs) expect(svg.getAttribute('aria-hidden')).toBe('true');
  });

  it('draws the shell from design tokens only, with no inline colors', () => {
    // The shell is the reference surface for the new palette: a hardcoded hex here
    // is a color that no theme can change.
    for (const file of ['./shell.html', './shell.css']) {
      const source = readFileSync(new URL(file, import.meta.url), 'utf8');
      expect(source, `${file} hardcodes a color`).not.toMatch(/#[0-9a-fA-F]{3,8}\b|\brgba?\(/);
    }
  });
});

describe('routeLabelFor', () => {
  it('names each section from its route', () => {
    expect(routeLabelFor('/')).toBe('Today');
    expect(routeLabelFor('/plants')).toBe('Plants');
    expect(routeLabelFor('/journal')).toBe('Journal');
    expect(routeLabelFor('/care')).toBe('Care');
    expect(routeLabelFor('/rounds?plant=fig')).toBe('Garden rounds');
    expect(routeLabelFor('/inbox')).toBe('Inbox');
    expect(routeLabelFor('/purchases')).toBe('Purchases');
    expect(routeLabelFor('/settings')).toBe('Settings');
  });

  it('names detail and form screens instead of showing a bare id', () => {
    expect(routeLabelFor('/plants/plant-7')).toBe('Plant detail');
    expect(routeLabelFor('/plants/new')).toBe('New plant');
    expect(routeLabelFor('/plants/plant-7/edit')).toBe('Edit plant');
    expect(routeLabelFor('/purchases/new')).toBe('New purchase');
    expect(routeLabelFor('/care/plan')).toBe('Care plan');
  });

  it('ignores query and fragment, and falls back to Today', () => {
    expect(routeLabelFor('/journal?page=2')).toBe('Journal');
    expect(routeLabelFor('/today#top')).toBe('Today');
    expect(routeLabelFor('/nope')).toBe('Today');
  });
});
