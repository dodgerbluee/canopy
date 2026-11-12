import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AccountsClient } from '../../core/auth/accounts';
import { SessionService } from '../../core/auth/session';
import { SetupPage } from './setup';

describe('First garden setup', () => {
  const request = vi.fn();
  beforeEach(() => {
    request.mockReset().mockResolvedValue({ setupRequired: true });
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: AccountsClient, useValue: { request } }],
    });
  });
  function render() {
    const fixture = TestBed.createComponent(SetupPage);
    fixture.detectChanges();
    return fixture;
  }
  function complete(page: SetupPage) {
    page.form.setValue({
      username: 'gardener',
      password: 'garden-passphrase',
      confirm: 'garden-passphrase',
      workspaceName: 'My trees',
      timezone: 'America/Chicago',
      currency: 'USD',
    });
  }
  it('keeps an existing garden out of registration', async () => {
    request.mockResolvedValue({ setupRequired: false });
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    render();
    await vi.waitFor(() => expect(navigate).toHaveBeenCalledWith('/login'));
  });
  it('keeps mismatched passwords local and sends no setup mutation', async () => {
    const page = render().componentInstance;
    complete(page);
    page.form.controls.confirm.setValue('different');
    await page.submit();
    expect(page.error()).toContain('do not match');
    expect(request.mock.calls.some(([path]) => path === 'setup')).toBe(false);
  });
  it('creates the account, refreshes the authenticated session and opens the garden', async () => {
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    const refresh = vi.spyOn(TestBed.inject(SessionService), 'refresh').mockResolvedValue('authed');
    const page = render().componentInstance;
    complete(page);
    await page.submit();
    expect(request).toHaveBeenCalledWith(
      'setup',
      'POST',
      expect.objectContaining({ username: 'gardener', workspaceName: 'My trees' }),
    );
    expect(request.mock.calls.find(([path]) => path === 'setup')?.[2]).not.toHaveProperty(
      'confirm',
    );
    expect(refresh).toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('/');
    expect(page.busy()).toBe(false);
  });
  it('creates an account with the collapsed garden defaults', async () => {
    vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    vi.spyOn(TestBed.inject(SessionService), 'refresh').mockResolvedValue('authed');
    const page = render().componentInstance;
    page.form.patchValue({
      username: 'gardener',
      password: 'garden-passphrase',
      confirm: 'garden-passphrase',
    });
    await page.submit();
    expect(page.preferencesOpen()).toBe(false);
    expect(request).toHaveBeenCalledWith(
      'setup',
      'POST',
      expect.objectContaining({ workspaceName: 'My garden', currency: 'USD' }),
    );
  });
  it('opens preferences when a hidden garden field is invalid', async () => {
    const page = render().componentInstance;
    complete(page);
    page.form.controls.workspaceName.setValue('');
    await page.submit();
    expect(page.preferencesOpen()).toBe(true);
    expect(request.mock.calls.some(([path]) => path === 'setup')).toBe(false);
  });
  it('requires ten password characters and sends no name field', async () => {
    vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
    vi.spyOn(TestBed.inject(SessionService), 'refresh').mockResolvedValue('authed');
    const page = render().componentInstance;
    page.form.patchValue({ username: 'gardener', password: '123456789', confirm: '123456789' });
    await page.submit();
    expect(request.mock.calls.some(([path]) => path === 'setup')).toBe(false);
    page.form.patchValue({ password: '1234567890', confirm: '1234567890' });
    await page.submit();
    const sent = request.mock.calls.find(([path]) => path === 'setup')?.[2];
    expect(sent).toMatchObject({ username: 'gardener', password: '1234567890' });
    expect(sent).not.toHaveProperty('displayName');
  });
});
