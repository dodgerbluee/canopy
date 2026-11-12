import { Injectable, signal } from '@angular/core';

export type Theme = 'light' | 'dark';

/** System preference with owner override. Override persists in localStorage
 * (nonsecret preference; allowed by the state contract). */
@Injectable({ providedIn: 'root' })
export class UiTheme {
  private readonly key = 'canopy-theme';
  readonly theme = signal<Theme>(this.initial());

  constructor() {
    this.apply();
    matchMedia('(prefers-color-scheme: light)').addEventListener?.('change', () => {
      if (!localStorage.getItem(this.key)) {
        this.theme.set(this.system());
        this.apply();
      }
    });
  }

  set(t: Theme | 'system'): void {
    if (t === 'system') {
      localStorage.removeItem(this.key);
      this.theme.set(this.system());
    } else {
      localStorage.setItem(this.key, t);
      this.theme.set(t);
    }
    this.apply();
  }

  private initial(): Theme {
    const saved = localStorage.getItem(this.key);
    if (saved === 'light' || saved === 'dark') return saved;
    return this.system();
  }
  private system(): Theme {
    return matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  private apply(): void {
    document.documentElement.dataset['theme'] = this.theme();
  }
}
