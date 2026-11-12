import { DestroyRef, Injectable, inject, signal } from '@angular/core';

export type Theme = 'light' | 'dark';

/** Follow this browser by default. Only an explicit choice is persisted. */
@Injectable({ providedIn: 'root' })
export class UiTheme {
  private readonly key = 'canopy-theme';
  private readonly systemQuery = matchMedia('(prefers-color-scheme: dark)');
  readonly theme = signal<Theme>(this.saved() ?? this.system());

  constructor() {
    this.apply();
    const changed = () => {
      if (this.saved() === null) {
        this.theme.set(this.system());
        this.apply();
      }
    };
    this.systemQuery.addEventListener?.('change', changed);
    inject(DestroyRef).onDestroy(() => this.systemQuery.removeEventListener?.('change', changed));
  }

  set(t: Theme | 'system'): void {
    try {
      if (t === 'system') localStorage.removeItem(this.key);
      else localStorage.setItem(this.key, t);
    } catch {
      // Browser privacy settings may disable storage; the current choice still applies.
    }
    this.theme.set(t === 'system' ? this.system() : t);
    this.apply();
  }

  private saved(): Theme | null {
    try {
      const saved = localStorage.getItem(this.key);
      return saved === 'light' || saved === 'dark' ? saved : null;
    } catch {
      return null;
    }
  }
  private system(): Theme {
    return this.systemQuery.matches ? 'dark' : 'light';
  }
  private apply(): void {
    document.documentElement.dataset['theme'] = this.theme();
    document.documentElement.style.colorScheme = this.theme();
  }
}
