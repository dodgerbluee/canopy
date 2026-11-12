import { TestBed } from '@angular/core/testing';
import { Location } from '@angular/common';
import { MockLocationStrategy } from '@angular/common/testing';
import { type CanActivateFn, Router, provideRouter } from '@angular/router';
import { describe, expect, it, vi } from 'vitest';
import { anonGuard, authGuard } from './guard';
import { SessionService } from './session';

/** Runs a functional guard with a settled session state and a real Router. */
async function runGuard(
  guard: CanActivateFn,
  setup: (session: SessionService) => void,
  url = '/settings',
) {
  TestBed.configureTestingModule({
    providers: [provideRouter([]), { provide: Location, useClass: MockLocationStrategy }],
  });
  setup(TestBed.inject(SessionService));
  const router = TestBed.inject(Router);
  return TestBed.runInInjectionContext(() =>
    guard(router.createUrlTree([url]).root as never, { url } as never),
  );
}

function authed(session: SessionService): void {
  session.state.set('authed');
  session.info.set({ workspaceId: 'w', expiresAt: 'x', csrfToken: 'c' });
}
function anon(session: SessionService): void {
  session.state.set('anon');
}
function expired(session: SessionService): void {
  session.state.set('anon');
  session.ended.set('expired');
}

describe('authGuard', () => {
  it('allows authed users', async () => {
    expect(await runGuard(authGuard, authed)).toBe(true);
  });

  it('redirects anon users to /login preserving returnUrl', async () => {
    const result = await runGuard(authGuard, anon);
    expect(result.toString()).toBe('/login?returnUrl=%2Fsettings');
  });

  it('flags expired sessions on the redirect so login can explain', async () => {
    const result = await runGuard(authGuard, expired);
    expect(result.toString()).toContain('returnUrl=%2Fsettings');
    expect(result.toString()).toContain('#expired');
  });

  it('resolves unknown state against the server first', async () => {
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: Location, useClass: MockLocationStrategy }],
    });
    const session = TestBed.inject(SessionService);
    const refresh = vi.spyOn(session, 'refresh').mockImplementation(async () => {
      session.state.set('authed');
      return 'authed';
    });
    const router = TestBed.inject(Router);
    const result = await TestBed.runInInjectionContext(() =>
      authGuard(router.createUrlTree(['/x']).root as never, { url: '/x' } as never),
    );
    expect(refresh).toHaveBeenCalled();
    expect(result).toBe(true);
  });
});

describe('anonGuard', () => {
  it('sends authed users home from /login', async () => {
    const result = await runGuard(anonGuard, authed, '/login');
    expect(result.toString()).toBe('/');
  });

  it('lets anon users through', async () => {
    expect(await runGuard(anonGuard, anon, '/login')).toBe(true);
  });
});
