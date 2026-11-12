import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { SessionService } from './session';

/** Server is the authority: unknown state is resolved via GET /api/auth/session
 * before any guarded route renders. Unauthenticated users land on /login with
 * their destination preserved. */
export const authGuard: CanActivateFn = async (_route, state) => {
  const session = inject(SessionService);
  const router = inject(Router);
  if (session.state() === 'unknown') await session.refresh();
  if (session.state() === 'authed') return true;
  return router.createUrlTree(['/login'], {
    queryParams: { returnUrl: state.url },
    ...(session.ended() === 'expired' ? { fragment: 'expired' } : {}),
  });
};

/** /login is only for signed-out visitors; signed-in users go home. */
export const anonGuard: CanActivateFn = async () => {
  const session = inject(SessionService);
  const router = inject(Router);
  if (session.state() === 'unknown') await session.refresh();
  return session.state() === 'authed' ? router.createUrlTree(['/']) : true;
};
