import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { AccountsClient, SignInOptions } from '../../core/auth/accounts';
import { SessionService } from '../../core/auth/session';
import { minimumPassword } from '../../core/auth/password';
import { UiIcon } from '../../ui/icon';

@Component({
  selector: 'setup-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, RouterLink, UiIcon],
  templateUrl: './setup.html',
  styleUrl: './setup.css',
})
export class SetupPage {
  private readonly accounts = inject(AccountsClient);
  private readonly session = inject(SessionService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  readonly available = signal(false);
  readonly claimExisting = signal(false);
  readonly busy = signal(false);
  readonly error = signal('');
  readonly attempted = signal(false);
  readonly preferencesOpen = signal(false);
  readonly form = inject(NonNullableFormBuilder).group({
    username: ['', [Validators.required, Validators.pattern(/^[a-z0-9][a-z0-9_.-]{1,63}$/)]],
    password: ['', [Validators.required, minimumPassword, Validators.maxLength(1024)]],
    confirm: ['', Validators.required],
    workspaceName: ['My garden', [Validators.required, Validators.maxLength(160)]],
    timezone: [Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', Validators.required],
    currency: ['USD', [Validators.required, Validators.pattern(/^[A-Z]{3}$/)]],
  });
  constructor() {
    void this.check();
  }
  async check(): Promise<void> {
    try {
      const token = this.route.snapshot.fragment;
      if (this.route.snapshot.routeConfig?.path === 'setup/claim' && token) {
        await this.accounts.request('setup/claim', 'POST', { token });
        await this.router.navigateByUrl('/setup/claim', { replaceUrl: true });
      }
      const options = await this.accounts.request<SignInOptions>('options');
      if (!options.setupRequired) {
        await this.router.navigateByUrl('/login');
        return;
      }
      this.claimExisting.set(options.claimExisting === true);
      this.available.set(true);
    } catch (error) {
      this.error.set((error as Error).message);
    }
  }
  preferencesToggled(event: Event): void {
    this.preferencesOpen.set((event.target as HTMLDetailsElement).open);
  }
  async submit(): Promise<void> {
    this.attempted.set(true);
    this.error.set('');
    if (this.form.invalid) {
      if (['workspaceName', 'timezone', 'currency'].some((name) => this.form.get(name)?.invalid))
        this.preferencesOpen.set(true);
      this.error.set(
        'Complete every field. Use a lowercase username and a password of at least 10 characters.',
      );
      return;
    }
    const { confirm, ...draft } = this.form.getRawValue();
    if (confirm !== draft.password) {
      this.error.set('Your passwords do not match.');
      return;
    }
    this.busy.set(true);
    try {
      await this.accounts.request('setup', 'POST', draft);
      if ((await this.session.refresh()) !== 'authed')
        throw new Error('Your account was created. Sign in to continue.');
      await this.router.navigateByUrl('/');
    } catch (error) {
      this.error.set((error as Error).message);
    } finally {
      this.busy.set(false);
    }
  }
}
