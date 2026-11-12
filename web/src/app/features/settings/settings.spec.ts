import { TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { UiTheme } from '../../ui/theme';
import { SettingsPage } from './settings';

const matchMediaStub = (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => {},
  removeEventListener: () => {},
  addListener: () => {},
  removeListener: () => {},
  dispatchEvent: () => false,
});

describe('SettingsPage', () => {
  let session: SessionService;
  let navigate: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    localStorage.clear();
    // jsdom has no matchMedia; UiTheme consults it for the 'system' option.
    vi.stubGlobal('matchMedia', matchMediaStub);
    navigate = vi.fn().mockResolvedValue(true);
    TestBed.configureTestingModule({
      providers: [provideRouter([])],
    });
    navigate = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1234567-x',
      expiresAt: '2026-11-02T00:00:00Z',
      csrfToken: 'c',
    });

    // The page reads the workspace through the GraphQL transport; stub the
    // envelope exactly as the gqlgen server shapes it.
    const sessionEnvelope = {
      data: {
        session: {
          authenticated: true,
          workspace: {
            id: 'w1234567-x',
            displayName: 'My garden',
            timezone: 'UTC',
            defaultCurrency: 'USD',
            settingsRevision: 1,
            location: null,
          },
        },
      },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/graphql') {
          return new Response(JSON.stringify(sessionEnvelope), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        return new Response('', { status: 200 });
      }),
    );
  });

  afterEach(() => vi.unstubAllGlobals());

  function render() {
    const fixture = TestBed.createComponent(SettingsPage);
    fixture.detectChanges();
    return fixture;
  }

  it('shows workspace values from the GraphQL session query; location stays unknown when unset', async () => {
    const fixture = render();
    await vi.waitFor(() => expect(fixture.componentInstance['workspace']()).not.toBeNull());
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.textContent).toContain('My garden');
    expect(root.textContent).toContain('UTC');
    expect(root.textContent).toContain('USD');
    const unknowns = [...root.querySelectorAll('.badge')].filter((b) =>
      b.textContent?.includes('Unknown'),
    );
    expect(unknowns.length).toBe(1); // location only — genuinely unset
  });

  it('theme radios switch the applied theme and persist only the preference', async () => {
    const fixture = render();
    const root = fixture.nativeElement as HTMLElement;
    const dark = [...root.querySelectorAll('input[type=radio]')].find(
      (r) => (r as HTMLInputElement).value === 'dark',
    ) as HTMLInputElement;
    dark.checked = true;
    dark.dispatchEvent(new Event('change'));
    await fixture.whenStable();
    expect(TestBed.inject(UiTheme).theme()).toBe('dark');
    expect(document.documentElement.dataset['theme']).toBe('dark');
    expect(localStorage.getItem('canopy-theme')).toBe('dark');
    // preference is not a secret; nothing else may be stored
    expect(Object.keys(localStorage)).toEqual(['canopy-theme']);
  });

  it('system clears the stored override', async () => {
    TestBed.inject(UiTheme).set('dark');
    const fixture = render();
    const root = fixture.nativeElement as HTMLElement;
    const sys = [...root.querySelectorAll('input[type=radio]')].find(
      (r) => (r as HTMLInputElement).value === 'system',
    ) as HTMLInputElement;
    sys.checked = true;
    sys.dispatchEvent(new Event('change'));
    await fixture.whenStable();
    expect(localStorage.getItem('canopy-theme')).toBeNull();
  });

  it('sign out revokes the session server-side and routes to /login', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const fixture = render();
    await fixture.componentInstance['signOut']();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/auth/logout',
      expect.objectContaining({ method: 'POST', headers: { 'X-CSRF-Token': 'c' } }),
    );
    expect(session.state()).toBe('anon');
    expect(navigate).toHaveBeenCalledWith('/login');
    vi.unstubAllGlobals();
  });
});
