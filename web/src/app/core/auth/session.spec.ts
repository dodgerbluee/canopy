import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from './session';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const SESSION_BODY = {
  authenticated: true,
  workspaceId: '11111111-2222-3333-4444-555555555555',
  expiresAt: '2026-11-02T00:00:00Z',
  csrfToken: 'csrf-abc-123',
};

describe('SessionService', () => {
  let service: SessionService;
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    service = TestBed.inject(SessionService);
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
    sessionStorage.clear();
  });

  it('starts unknown and never touches storage', async () => {
    expect(service.state()).toBe('unknown');
    await service.refresh();
    expect(fetchMock).toHaveBeenCalledWith('/api/auth/session', expect.objectContaining({ credentials: 'same-origin' }));
  });

  it('login success moves to authed and keeps the csrf token in memory only', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSION_BODY));
    const result = await service.login('pw');
    expect(result).toEqual({ ok: true });
    expect(service.state()).toBe('authed');
    expect(service.info()?.csrfToken).toBe('csrf-abc-123');
    // no secrets in browser persistent storage
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it('login posts JSON and maps 401 to a generic credential failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { status: 401, title: 'Invalid credentials' }));
    const result = await service.login('bad');
    expect(result).toEqual({ ok: false, code: 'INVALID_CREDENTIALS' });
    expect(service.state()).toBe('unknown');
    const [, init] = fetchMock.mock.calls[0];
    expect(init.headers).toEqual({ 'Content-Type': 'application/json' });
  });

  it('login maps 429 to RATE_LIMITED', async () => {
    fetchMock.mockResolvedValue(jsonResponse(429, {}));
    expect(await service.login('pw')).toEqual({ ok: false, code: 'RATE_LIMITED' });
  });

  it('login maps network failure to NETWORK', async () => {
    fetchMock.mockRejectedValue(new TypeError('offline'));
    expect(await service.login('pw')).toEqual({ ok: false, code: 'NETWORK' });
  });

  it('login maps server 5xx to NETWORK, not a credential failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(502, {}));
    expect(await service.login('pw')).toEqual({ ok: false, code: 'NETWORK' });
  });

  it('refresh resolves anon without claiming expiry on a cold visit', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, {}));
    expect(await service.refresh()).toBe('anon');
    expect(service.ended()).toBeNull();
  });

  it('refresh after a live session marks expiry', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSION_BODY));
    await service.login('pw');
    fetchMock.mockResolvedValue(jsonResponse(401, {}));
    expect(await service.refresh()).toBe('anon');
    expect(service.ended()).toBe('expired');
  });

  it('a stale cookie reported by the server marks expiry even on a cold load', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(401, { problems: [{ code: 'SESSION_EXPIRED', message: 'Not authenticated' }] }),
    );
    expect(await service.refresh()).toBe('anon');
    expect(service.ended()).toBe('expired');
  });

  it('logout sends the csrf header and clears state', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSION_BODY));
    await service.login('pw');
    fetchMock.mockResolvedValue(jsonResponse(200, null));
    await service.logout();
    expect(service.state()).toBe('anon');
    expect(service.info()).toBeNull();
    expect(service.ended()).toBe('logout');
    const [, init] = fetchMock.mock.calls[1];
    expect(init.headers).toEqual({ 'X-CSRF-Token': 'csrf-abc-123' });
  });

  it('markExpired clears the session for any 401 from the data layer', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSION_BODY));
    await service.login('pw');
    service.markExpired();
    expect(service.state()).toBe('anon');
    expect(service.ended()).toBe('expired');
  });
});
