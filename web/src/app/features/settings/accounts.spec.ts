import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AccountsClient, GardenUser } from '../../core/auth/accounts';
import { AccountSettings } from './accounts';

const member: GardenUser = {
  id: 'member',
  username: 'helper',
  displayName: 'Helper',
  role: 'MEMBER',
  disabled: false,
  primary: false,
  hasPassword: true,
  oidcLinked: false,
};
const admin: GardenUser = {
  ...member,
  id: 'admin',
  username: 'owner',
  role: 'ADMIN',
  primary: true,
};
describe('Account settings', () => {
  const request = vi.fn();
  beforeEach(() => {
    request.mockReset();
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: AccountsClient, useValue: { request } }],
    });
  });
  it('shows members their sign-in controls without loading administrator settings', async () => {
    request.mockImplementation(async (path: string) =>
      path === 'me' ? member : { setupRequired: false, oidcName: 'Garden SSO' },
    );
    const fixture = TestBed.createComponent(AccountSettings);
    fixture.detectChanges();
    await vi.waitFor(() => expect(fixture.componentInstance.loading()).toBe(false));
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.textContent).toContain('Helper');
    expect(root.textContent).toContain('Connect Garden SSO');
    expect(root.textContent).not.toContain('People in your garden');
    expect(request.mock.calls.map(([path]) => path)).toEqual(['me', 'options']);
  });
  it('preserves a failed account form so it can be corrected', async () => {
    request.mockImplementation(async (path: string) =>
      path === 'me' ? admin : path === 'users' ? [admin] : { revision: 0 },
    );
    const fixture = TestBed.createComponent(AccountSettings);
    fixture.detectChanges();
    await vi.waitFor(() => expect(fixture.componentInstance.loading()).toBe(false));
    const page = fixture.componentInstance;
    page.editUser();
    page.userDraft = {
      username: 'helper',
      displayName: 'Helper',
      role: 'MEMBER',
      disabled: false,
      password: 'helper-passphrase',
    };
    request.mockRejectedValue(new Error('Username already exists.'));
    await page.saveUser();
    expect(page.userOpen()).toBe(true);
    expect(page.formError()).toContain('Username');
    expect(page.userDraft.username).toBe('helper');
    expect(page.busy()).toBe(false);
  });
  it('clears the client secret after saving and reloads only sanitized settings', async () => {
    request.mockImplementation(async (path: string) =>
      path === 'me'
        ? admin
        : path === 'users'
          ? [admin]
          : {
              displayName: 'SSO',
              issuer: 'https://auth.example.com',
              clientId: 'canopy',
              hasSecret: true,
              revision: 1,
            },
    );
    const fixture = TestBed.createComponent(AccountSettings);
    fixture.detectChanges();
    await vi.waitFor(() => expect(fixture.componentInstance.loading()).toBe(false));
    const page = fixture.componentInstance;
    page.editOIDC();
    page.oidcDraft.clientSecret = 'private-secret';
    await page.saveOIDC();
    expect(page.oidcDraft.clientSecret).toBe('');
    expect(page.oidcOpen()).toBe(false);
    expect(page.connection()).not.toHaveProperty('clientSecret');
  });
});
