import { Injectable, signal } from '@angular/core';
import type { GardenUser } from './accounts';

export type SessionState = 'unknown' | 'anon' | 'authed';

/** Why the last authenticated session ended, for honest login-page notices. */
export type SessionEnd = 'logout' | 'expired' | null;

export interface SessionInfo {
  user?: GardenUser;
  workspaceId: string;
  expiresAt: string;
  /** Held in memory only. Never written to localStorage/sessionStorage. */
  csrfToken: string;
}

export type LoginResult =
  { ok: true } | { ok: false; code: 'INVALID_CREDENTIALS' | 'RATE_LIMITED' | 'NETWORK' };

/**
 * Client-side view of the server session (T06 transport: /api/auth/*).
 * The session cookie is HttpOnly; the CSRF token lives only in memory, so
 * nothing secret is ever placed in browser persistent storage.
 */
@Injectable({ providedIn: 'root' })
export class SessionService {
  readonly state = signal<SessionState>('unknown');
  readonly info = signal<SessionInfo | null>(null);
  readonly ended = signal<SessionEnd>(null);

  async login(password: string, username?: string): Promise<LoginResult> {
    let res: Response;
    try {
      res = await fetch('/api/auth/login', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password, ...(username === undefined ? {} : { username }) }),
      });
    } catch {
      return { ok: false, code: 'NETWORK' };
    }
    if (res.status === 429) return { ok: false, code: 'RATE_LIMITED' };
    if (res.status >= 500) return { ok: false, code: 'NETWORK' };
    if (!res.ok) return { ok: false, code: 'INVALID_CREDENTIALS' };
    this.info.set(this.readInfo(await res.json()));
    this.state.set('authed');
    this.ended.set(null);
    return { ok: true };
  }

  /** Re-reads the server session (bootstrap on load, deep link, refresh). */
  async refresh(): Promise<SessionState> {
    try {
      const res = await fetch('/api/auth/session', { credentials: 'same-origin' });
      if (res.ok) {
        this.info.set(this.readInfo(await res.json()));
        this.state.set('authed');
        this.ended.set(null);
      } else {
        // a stale cookie the browser itself presented is reported as
        // SESSION_EXPIRED; a cold anonymous visit must not show the notice
        let expired = this.state() === 'authed';
        try {
          const data = await res.json();
          if (data?.problems?.some((p: { code?: string }) => p.code === 'SESSION_EXPIRED')) {
            expired = true;
          }
        } catch {
          // non-JSON failure: treat as anonymous, not expired
        }
        this.clear(expired ? 'expired' : null);
      }
    } catch {
      this.clear(null);
    }
    return this.state();
  }

  async logout(): Promise<void> {
    const csrf = this.info()?.csrfToken;
    try {
      await fetch('/api/auth/logout', {
        method: 'POST',
        credentials: 'same-origin',
        headers: csrf ? { 'X-CSRF-Token': csrf } : {},
      });
    } catch {
      // local teardown happens regardless; the cookie dies with the browser
    }
    this.clear('logout');
  }

  /** Any API call returning 401 means the session died server-side. */
  markExpired(): void {
    this.clear('expired');
  }

  private clear(end: SessionEnd): void {
    this.info.set(null);
    this.state.set('anon');
    this.ended.set(end);
  }

  private readInfo(data: {
    workspaceId: string;
    expiresAt: string;
    csrfToken: string;
    user?: GardenUser;
  }): SessionInfo {
    return {
      workspaceId: data.workspaceId,
      expiresAt: data.expiresAt,
      csrfToken: data.csrfToken,
      ...(data.user ? { user: data.user } : {}),
    };
  }
}
