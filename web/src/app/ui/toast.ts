import { Component, Injectable, signal, inject } from '@angular/core';

export interface Toast {
  id: number;
  message: string;
  kind: 'ok' | 'error';
}

/** Toasts never steal focus. Successes announce politely; errors assertively. */
@Injectable({ providedIn: 'root' })
export class UiToasts {
  private next = 1;
  private readonly timeout = 6000;
  readonly toasts = signal<Toast[]>([]);

  ok(message: string): void {
    this.push(message, 'ok');
  }
  error(message: string): void {
    this.push(message, 'error');
  }
  private push(message: string, kind: Toast['kind']): void {
    const t: Toast = { id: this.next++, message, kind };
    this.toasts.update((ts) => [...ts, t]);
    setTimeout(() => this.dismiss(t.id), this.timeout);
  }
  dismiss(id: number): void {
    this.toasts.update((ts) => ts.filter((t) => t.id !== id));
  }
}

@Component({
  selector: 'ui-toast-region',
  template: `
    <div class="toast-region" aria-live="polite" role="status" aria-label="Notifications">
      @for (t of polite(); track t.id) {
        <div class="toast">{{ t.message }}</div>
      }
    </div>
    <div class="toast-region" aria-live="assertive" role="alert" aria-label="Errors">
      @for (t of errors(); track t.id) {
        <div class="toast toast-error">{{ t.message }}</div>
      }
    </div>
  `,
})
export class UiToastRegion {
  private readonly toasts = inject(UiToasts);
  protected polite = () => this.toasts.toasts().filter((t) => t.kind === 'ok');
  protected errors = () => this.toasts.toasts().filter((t) => t.kind === 'error');
}
