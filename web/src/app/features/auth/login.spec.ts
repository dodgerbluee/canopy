import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, Router, provideRouter } from '@angular/router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { LoginPage } from './login';

/** The unit-test environment lacks matchMedia; UiTheme needs it. */
function stubMatchMedia(): void {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}
stubMatchMedia();

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

describe('LoginPage', () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let navigate: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    navigate = vi.fn().mockResolvedValue(true);
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        { provide: Router, useValue: { navigateByUrl: navigate, createUrlTree: vi.fn() } },
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { queryParamMap: new Map([['returnUrl', '/settings']]) } },
        },
      ],
    });
  });

  function render() {
    const fixture = TestBed.createComponent(LoginPage);
    fixture.detectChanges();
    return fixture;
  }

  it('blocks submit and shows a field error without calling the server when empty', async () => {
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    el.querySelector('form')!.dispatchEvent(new Event('submit'));
    fixture.detectChanges();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(el.querySelector('.field-error')?.textContent).toContain('Enter your password');
  });

  it('shows the generic server error on bad credentials', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, {}));
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector('#password') as HTMLInputElement;
    input.value = 'wrong';
    input.dispatchEvent(new Event('input'));
    el.querySelector('form')!.dispatchEvent(new Event('submit'));
    await vi.waitFor(() => expect(el.querySelector('[role="alert"]')).toBeTruthy());
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Invalid credentials');
    expect(navigate).not.toHaveBeenCalled();
  });

  it('shows a rate-limit notice on 429', async () => {
    fetchMock.mockResolvedValue(jsonResponse(429, {}));
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector('#password') as HTMLInputElement;
    input.value = 'pw';
    input.dispatchEvent(new Event('input'));
    el.querySelector('form')!.dispatchEvent(new Event('submit'));
    await vi.waitFor(() => expect(el.querySelector('[role="alert"]')).toBeTruthy());
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Too many attempts');
  });

  it('navigates to returnUrl after a successful login', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, { authenticated: true, workspaceId: 'w', expiresAt: 'e', csrfToken: 'c' }),
    );
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector('#password') as HTMLInputElement;
    input.value = 'pw';
    input.dispatchEvent(new Event('input'));
    el.querySelector('form')!.dispatchEvent(new Event('submit'));
    await vi.waitFor(() => expect(navigate).toHaveBeenCalledWith('/settings'));
  });

  it('explains an expired session when the guard sent us here', async () => {
    const session = TestBed.inject(SessionService);
    session.state.set('anon');
    session.ended.set('expired');
    const fixture = render();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('[role="status"]')?.textContent).toContain('session ended');
  });
});
