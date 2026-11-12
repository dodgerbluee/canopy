import { TestBed } from '@angular/core/testing';
import { UiToastRegion, UiToasts } from './toast';

describe('UiToasts', () => {
  it('successes announce via a polite live region; errors via assertive', () => {
    const fixture = TestBed.createComponent(UiToastRegion);
    const toasts = TestBed.inject(UiToasts);
    toasts.ok('Saved');
    toasts.error('Save failed');
    fixture.detectChanges();

    const polite = document.querySelector('[role="status"][aria-live="polite"]')!;
    const alert = document.querySelector('[role="alert"][aria-live="assertive"]')!;
    expect(polite.textContent).toContain('Saved');
    expect(alert.textContent).toContain('Save failed');
  });

  it('never steals focus', () => {
    const fixture = TestBed.createComponent(UiToastRegion);
    const trigger = document.createElement('button');
    document.body.append(trigger);
    trigger.focus();
    const before = document.activeElement;

    TestBed.inject(UiToasts).ok('Saved');
    fixture.detectChanges();

    expect(document.activeElement).toBe(before);
    trigger.remove();
  });
});
