import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { SessionService } from '../../core/auth/session';
import { UiTheme } from '../../ui/theme';
import { UiIcon } from '../../ui/icon';

/**
 * Typed reactive login. The server returns one generic credential error for
 * every failure mode; the UI mirrors that (no user enumeration).
 */
@Component({
  selector: 'login-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ReactiveFormsModule, UiIcon],
  templateUrl: './login.html',
  styleUrl: './login.css',
})
export class LoginPage {
  protected readonly theme = inject(UiTheme);
  private readonly session = inject(SessionService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  protected readonly form = inject(NonNullableFormBuilder).group({
    password: ['', [Validators.required, Validators.maxLength(1024)]],
  });
  protected readonly busy = signal(false);
  protected readonly serverError = signal<string | null>(null);
  protected readonly showFieldError = signal(false);
  protected readonly errorId = signal('password-error');

  protected toggleTheme(): void {
    this.theme.set(this.theme.theme() === 'dark' ? 'light' : 'dark');
  }

  protected async submit(): Promise<void> {
    if (this.form.invalid) {
      this.showFieldError.set(true);
      return;
    }
    this.busy.set(true);
    this.serverError.set(null);
    const result = await this.session.login(this.form.controls.password.value);
    this.busy.set(false);
    if (result.ok) {
      const returnUrl = this.route.snapshot.queryParamMap.get('returnUrl') ?? '/';
      await this.router.navigateByUrl(returnUrl);
      return;
    }
    this.serverError.set(
      result.code === 'RATE_LIMITED'
        ? 'Too many attempts — wait a few minutes and try again.'
        : result.code === 'NETWORK'
          ? 'Network error — check your connection and try again.'
          : 'Invalid credentials.',
    );
  }
}
