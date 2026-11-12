import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UiTheme } from './theme';

describe('Browser theme preference', () => {
  let dark: boolean;
  let changed: (() => void) | undefined;
  beforeEach(() => {
    localStorage.clear();
    dark = false;
    changed = undefined;
    vi.stubGlobal('matchMedia', (query: string) => ({
      media: query,
      get matches() {
        return dark;
      },
      addEventListener: (_: string, listener: () => void) => {
        changed = listener;
      },
      removeEventListener: vi.fn(),
    }));
    TestBed.configureTestingModule({});
  });
  afterEach(() => {
    TestBed.resetTestingModule();
    vi.unstubAllGlobals();
    localStorage.clear();
    delete document.documentElement.dataset['theme'];
    document.documentElement.style.colorScheme = '';
  });
  it('uses dark browser preference without writing an override', () => {
    dark = true;
    const theme = TestBed.inject(UiTheme);
    expect(theme.theme()).toBe('dark');
    expect(document.documentElement.dataset['theme']).toBe('dark');
    expect(localStorage.getItem('canopy-theme')).toBeNull();
  });
  it('uses light when the browser does not prefer dark', () => {
    expect(TestBed.inject(UiTheme).theme()).toBe('light');
  });
  it('follows browser changes live while system is selected', () => {
    const theme = TestBed.inject(UiTheme);
    dark = true;
    changed?.();
    expect(theme.theme()).toBe('dark');
    dark = false;
    changed?.();
    expect(theme.theme()).toBe('light');
  });
  it('respects an explicit saved choice and resumes following the browser with System', () => {
    localStorage.setItem('canopy-theme', 'light');
    dark = true;
    const theme = TestBed.inject(UiTheme);
    expect(theme.theme()).toBe('light');
    changed?.();
    expect(theme.theme()).toBe('light');
    theme.set('system');
    expect(theme.theme()).toBe('dark');
    expect(localStorage.getItem('canopy-theme')).toBeNull();
  });
  it('ignores malformed saved preferences', () => {
    localStorage.setItem('canopy-theme', 'invalid');
    const theme = TestBed.inject(UiTheme);
    dark = true;
    changed?.();
    expect(theme.theme()).toBe('dark');
  });
});
