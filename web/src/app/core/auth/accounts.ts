import { Injectable, inject } from '@angular/core';
import { SessionService } from './session';

export interface GardenUser {
  id: string;
  username: string;
  displayName: string;
  role: 'ADMIN' | 'MEMBER';
  disabled: boolean;
  primary: boolean;
  hasPassword: boolean;
  oidcLinked: boolean;
}
export interface SignInOptions {
  setupRequired: boolean;
  claimExisting?: boolean;
  oidcName: string;
}
export interface OIDCSettings {
  displayName: string;
  issuer: string;
  clientId: string;
  hasSecret: boolean;
  enabled: boolean;
  autoJoin: boolean;
  revision: number;
  callbackUrl: string;
}
export interface UserDraft {
  username: string;
  displayName: string;
  role: 'ADMIN' | 'MEMBER';
  disabled: boolean;
  password: string;
}
export interface SetupDraft {
  username: string;
  displayName?: string;
  password: string;
  workspaceName: string;
  timezone: string;
  currency: string;
}

@Injectable({ providedIn: 'root' })
export class AccountsClient {
  private readonly session = inject(SessionService);
  async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
    let response: Response;
    try {
      response = await fetch('/api/auth/' + path, {
        method,
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          ...(this.session.info()?.csrfToken
            ? { 'X-CSRF-Token': this.session.info()!.csrfToken }
            : {}),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    } catch {
      throw new Error('Could not reach Canopy. Check your connection and try again.');
    }
    const data = await response.json().catch(() => null);
    if (!response.ok) {
      if (
        response.status === 401 &&
        this.session.state() === 'authed' &&
        data?.problems?.some(
          (p: { code?: string }) => p.code === 'UNAUTHENTICATED' || p.code === 'SESSION_EXPIRED',
        )
      )
        this.session.markExpired();
      throw new Error(
        data?.problems?.[0]?.message || 'This request could not be completed. Try again.',
      );
    }
    return data as T;
  }
}
