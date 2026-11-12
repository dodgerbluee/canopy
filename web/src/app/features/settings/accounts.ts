import { ChangeDetectionStrategy, Component, inject, signal, viewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import {
  AccountsClient,
  GardenUser,
  OIDCSettings,
  SignInOptions,
  UserDraft,
} from '../../core/auth/accounts';
import { SessionService } from '../../core/auth/session';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { hasMinimumPassword } from '../../core/auth/password';
import { UiIcon } from '../../ui/icon';

@Component({
  selector: 'account-settings',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [FormsModule, UiEntryDialog, UiIcon],
  templateUrl: './accounts.html',
  styleUrl: './accounts.css',
})
export class AccountSettings {
  private readonly api = inject(AccountsClient);
  private readonly session = inject(SessionService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  readonly me = signal<GardenUser | null>(null);
  readonly users = signal<GardenUser[]>([]);
  readonly connection = signal<OIDCSettings | null>(null);
  readonly ssoName = signal('');
  readonly error = signal('');
  readonly notice = signal('');
  readonly formError = signal('');
  readonly busy = signal(false);
  readonly loading = signal(true);
  readonly userOpen = signal(false);
  readonly oidcOpen = signal(false);
  readonly passwordOpen = signal(false);
  readonly userEntry = viewChild<UiEntryDialog>('userEntry');
  readonly oidcEntry = viewChild<UiEntryDialog>('oidcEntry');
  readonly passwordEntry = viewChild<UiEntryDialog>('passwordEntry');
  editing: string | null = null;
  userDraft: UserDraft = this.blankUser();
  oidcDraft = {
    displayName: 'Single sign-on',
    issuer: '',
    clientId: '',
    clientSecret: '',
    enabled: false,
    autoJoin: false,
    revision: 0,
  };
  passwords = { currentPassword: '', newPassword: '', confirm: '' };

  constructor() {
    void this.load().then(() => {
      const status = this.route.snapshot.queryParamMap.get('oidc');
      if (status === 'linked')
        this.notice.set('SSO connected to your account. You can now use it to sign in.');
      if (status === 'failed')
        this.error.set('SSO could not be connected. Check the connection settings and try again.');
    });
  }
  private blankUser(): UserDraft {
    return { username: '', displayName: '', role: 'MEMBER', disabled: false, password: '' };
  }
  async load(): Promise<void> {
    this.error.set('');
    this.loading.set(true);
    try {
      const me = await this.api.request<GardenUser>('me');
      this.me.set(me);
      this.session.info.update((current) => (current ? { ...current, user: me } : current));
      if (me.role === 'ADMIN') {
        const [users, connection] = await Promise.all([
          this.api.request<GardenUser[]>('users'),
          this.api.request<OIDCSettings>('oidc/settings'),
        ]);
        this.users.set(users);
        this.connection.set(connection);
        this.ssoName.set(connection.revision ? connection.displayName : '');
      } else {
        const options = await this.api.request<SignInOptions>('options');
        this.ssoName.set(options.oidcName);
      }
    } catch (error) {
      this.error.set((error as Error).message);
    } finally {
      this.loading.set(false);
    }
  }
  editUser(user?: GardenUser): void {
    this.formError.set('');
    this.editing = user?.id ?? null;
    this.userDraft = user
      ? {
          username: user.username,
          displayName: user.displayName,
          role: user.role,
          disabled: user.disabled,
          password: '',
        }
      : this.blankUser();
    this.userOpen.set(true);
  }
  async saveUser(): Promise<void> {
    this.formError.set('');
    if (
      !/^[a-z0-9][a-z0-9_.-]{1,63}$/.test(this.userDraft.username) ||
      !this.userDraft.displayName.trim()
    ) {
      this.formError.set(
        'Enter a name and a lowercase username (2–64 letters, numbers, dots, dashes or underscores).',
      );
      return;
    }
    if (
      (!this.editing || this.userDraft.password) &&
      !hasMinimumPassword(this.userDraft.password)
    ) {
      this.formError.set('Use a password of at least 10 characters.');
      return;
    }
    this.busy.set(true);
    try {
      await this.api.request(
        this.editing ? 'users/' + this.editing : 'users',
        this.editing ? 'PUT' : 'POST',
        this.userDraft,
      );
      this.userDraft.password = '';
      this.userEntry()?.markPristine();
      this.userOpen.set(false);
      this.notice.set(
        this.editing
          ? 'Account updated.'
          : 'Account created. Share the username and initial password securely.',
      );
      if (this.editing === this.me()?.id) {
        if ((await this.session.refresh()) !== 'authed') {
          await this.router.navigateByUrl('/login');
          return;
        }
      }
      await this.load();
    } catch (error) {
      this.formError.set((error as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
  editOIDC(): void {
    const c = this.connection();
    this.formError.set('');
    this.oidcDraft = {
      displayName: c?.displayName || 'Single sign-on',
      issuer: c?.issuer || '',
      clientId: c?.clientId || '',
      clientSecret: '',
      enabled: c?.enabled || false,
      autoJoin: c?.autoJoin || false,
      revision: c?.revision || 0,
    };
    this.oidcOpen.set(true);
  }
  async saveOIDC(): Promise<void> {
    this.busy.set(true);
    this.formError.set('');
    try {
      this.connection.set(
        await this.api.request<OIDCSettings>('oidc/settings', 'PUT', this.oidcDraft),
      );
      this.oidcDraft.clientSecret = '';
      this.oidcEntry()?.markPristine();
      this.oidcOpen.set(false);
      this.notice.set(
        'SSO connection saved. Test discovery, then connect your account to verify sign-in.',
      );
      await this.load();
    } catch (error) {
      this.formError.set((error as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
  async testOIDC(): Promise<void> {
    this.busy.set(true);
    this.error.set('');
    this.notice.set('');
    try {
      this.notice.set((await this.api.request<{ message: string }>('oidc/test', 'POST')).message);
    } catch (error) {
      this.error.set((error as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
  async connect(): Promise<void> {
    this.busy.set(true);
    this.error.set('');
    try {
      const result = await this.api.request<{ url: string }>('oidc/link', 'POST');
      window.location.assign(result.url);
    } catch (error) {
      this.error.set((error as Error).message);
      this.busy.set(false);
    }
  }
  async disconnect(): Promise<void> {
    this.busy.set(true);
    this.error.set('');
    try {
      await this.api.request('oidc/unlink', 'POST');
      this.notice.set('SSO disconnected from your account.');
      await this.load();
    } catch (error) {
      this.error.set((error as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
  editPassword(): void {
    this.passwords = { currentPassword: '', newPassword: '', confirm: '' };
    this.formError.set('');
    this.passwordOpen.set(true);
  }
  async changePassword(): Promise<void> {
    this.formError.set('');
    if (!hasMinimumPassword(this.passwords.newPassword)) {
      this.formError.set('Use at least 10 characters.');
      return;
    }
    if (this.passwords.newPassword !== this.passwords.confirm) {
      this.formError.set('Your passwords do not match.');
      return;
    }
    this.busy.set(true);
    try {
      const { confirm: _confirm, ...draft } = this.passwords;
      await this.api.request('password', 'POST', draft);
      await this.session.refresh();
      this.passwords = { currentPassword: '', newPassword: '', confirm: '' };
      this.passwordEntry()?.markPristine();
      this.passwordOpen.set(false);
      this.notice.set('Password changed. Your other sessions have been signed out.');
    } catch (error) {
      this.formError.set((error as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
}
