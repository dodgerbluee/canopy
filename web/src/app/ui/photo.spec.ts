import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UiPhoto } from './photo';

@Component({
  standalone: true,
  imports: [UiPhoto],
  template: `
    <ui-photo
      [src]="src()"
      [alt]="alt()"
      [category]="category()"
      [ratio]="ratio()"
      [compact]="compact()"
      [framing]="framing()"
    />
  `,
})
class Host {
  readonly src = signal<string | null>('/v1/photos/9b2f/data');
  readonly alt = signal('Monstera leaf close-up');
  readonly category = signal('plant');
  readonly ratio = signal('');
  readonly compact = signal(false);
  readonly framing = signal<'auto' | 'full' | 'center'>('center');
}

describe('UiPhoto', () => {
  let fixture: ReturnType<typeof TestBed.createComponent<Host>>;
  let root: HTMLElement;
  let host: Host;

  beforeEach(() => {
    TestBed.configureTestingModule({ imports: [Host] });
    fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    root = fixture.nativeElement as HTMLElement;
    host = fixture.componentInstance;
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function photo(): HTMLElement {
    return root.querySelector('ui-photo')!;
  }

  it('renders the real image with the alt text the caller supplies', () => {
    const img = photo().querySelector('img');
    expect(img?.getAttribute('src')).toBe('/v1/photos/9b2f/data');
    expect(img?.getAttribute('alt')).toBe('Monstera leaf close-up');
    expect(img?.getAttribute('decoding')).toBe('async');
  });

  it('names the image from its category when no alt was supplied', () => {
    host.alt.set('');
    fixture.detectChanges();
    expect(photo().querySelector('img')?.getAttribute('alt')).toBe('plant photo');
  });

  it('renders a labelled placeholder and no image element when there is no photo', () => {
    host.src.set(null);
    fixture.detectChanges();
    expect(photo().querySelector('img')).toBeNull();
    expect(photo().textContent).toContain('No plant photo yet');
    // The sprout drawing is decoration, not content.
    expect(photo().querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
    expect(photo().querySelector('svg path')?.getAttribute('d')).toBeTruthy();
  });

  it('says what is missing for each kind of photo', () => {
    host.src.set(null);
    host.category.set('receipt');
    fixture.detectChanges();
    expect(photo().textContent).toContain('No receipt photo yet');

    host.category.set('');
    fixture.detectChanges();
    expect(photo().textContent).toContain('No photo yet');
  });

  it('falls back to the placeholder when the image fails to load', () => {
    photo().querySelector('img')!.dispatchEvent(new Event('error'));
    fixture.detectChanges();
    expect(photo().querySelector('img')).toBeNull();
    expect(photo().textContent).toContain('Photo could not be loaded');
  });

  it('goes back to the category label once the src is cleared after a failure', () => {
    photo().querySelector('img')!.dispatchEvent(new Event('error'));
    fixture.detectChanges();
    expect(photo().textContent).toContain('Photo could not be loaded');

    // the record no longer has a photo: the stale failure must not keep claiming
    // a load error for a src that is no longer being shown
    host.src.set(null);
    fixture.detectChanges();
    expect(photo().querySelector('img')).toBeNull();
    expect(photo().textContent).toContain('No plant photo yet');
    expect(photo().textContent).not.toContain('could not be loaded');
  });

  it('does not re-request a photo that already failed', () => {
    photo().querySelector('img')!.dispatchEvent(new Event('error'));
    fixture.detectChanges();
    // Any later change-detection pass must not resurrect the broken image, which
    // would otherwise fire a failing request per cycle.
    fixture.detectChanges();
    expect(photo().querySelector('img')).toBeNull();
  });

  it('shows a new photo after a failure, so a fresh src is not swallowed', () => {
    photo().querySelector('img')!.dispatchEvent(new Event('error'));
    fixture.detectChanges();
    host.src.set('/v1/photos/9b2f/data?after=retry');
    fixture.detectChanges();
    expect(photo().querySelector('img')?.getAttribute('src')).toBe(
      '/v1/photos/9b2f/data?after=retry',
    );
    expect(photo().textContent).not.toContain('could not be loaded');
  });

  it('keeps the placeholder label in the accessible name for compact thumbnails', () => {
    host.src.set(null);
    host.compact.set(true);
    fixture.detectChanges();
    const label = photo().querySelector('.ph-label')!;
    expect(label.classList.contains('visually-only')).toBe(true);
    expect(label.textContent).toContain('No plant photo yet');

    host.compact.set(false);
    fixture.detectChanges();
    expect(photo().querySelector('.ph-label')!.classList.contains('visually-only')).toBe(false);
  });

  it('carries the caller ratio and compact flag onto the box for the grid to lay out', () => {
    host.ratio.set('4/3');
    host.compact.set(true);
    fixture.detectChanges();
    const el = photo();
    expect(el.style.getPropertyValue('--photo-ratio')).toBe('4/3');
    expect(el.getAttribute('data-compact')).toBe('');

    host.ratio.set('');
    host.compact.set(false);
    fixture.detectChanges();
    expect(photo().hasAttribute('data-compact')).toBe(false);
    expect(photo().style.getPropertyValue('--photo-ratio')).toBe('');
  });

  it('shows the full original composition for documentary images', () => {
    host.framing.set('full');
    fixture.detectChanges();
    const img = photo().querySelector('img')!;
    expect(img.style.objectFit).toBe('contain');
    expect(img.style.objectPosition).toBe('50% 50%');
  });

  it('handles unavailable canvas safely and recalculates a crop when its box resizes', () => {
    let notify: ResizeObserverCallback = () => {};
    const disconnect = vi.fn();
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(callback: ResizeObserverCallback) {
          notify = callback;
        }
        observe() {}
        disconnect = disconnect;
      },
    );
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    host.src.set('/qa/no-canvas-resize.jpg');
    host.framing.set('auto');
    fixture.detectChanges();
    const img = photo().querySelector('img')!;
    let boxWidth = 200;
    let boxHeight = 100;
    Object.defineProperties(img, {
      naturalWidth: { value: 100, configurable: true },
      naturalHeight: { value: 400, configurable: true },
      clientWidth: { get: () => boxWidth, configurable: true },
      clientHeight: { get: () => boxHeight, configurable: true },
    });
    img.dispatchEvent(new Event('load'));
    fixture.detectChanges();
    const initial = img.style.objectPosition;
    expect(initial).not.toBe('50% 50%');
    expect(img.style.objectFit).toBe('cover');
    boxWidth = 100;
    boxHeight = 400;
    notify([], {} as ResizeObserver);
    fixture.detectChanges();
    expect(img.style.objectPosition).toBe('50% 50%');
    fixture.destroy();
    expect(disconnect).toHaveBeenCalled();
    expect(() => notify([], {} as ResizeObserver)).not.toThrow();
  });

  it('does not sample pixels in full-image mode', () => {
    const context = vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    host.framing.set('full');
    fixture.detectChanges();
    photo().querySelector('img')!.dispatchEvent(new Event('load'));
    fixture.detectChanges();
    expect(context).not.toHaveBeenCalled();
  });

  it('resets automatic framing when the image source changes', () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    host.framing.set('auto');
    host.src.set('/qa/first.jpg');
    fixture.detectChanges();
    const img = photo().querySelector('img')!;
    Object.defineProperties(img, {
      naturalWidth: { value: 100, configurable: true },
      naturalHeight: { value: 400, configurable: true },
      clientWidth: { value: 200, configurable: true },
      clientHeight: { value: 100, configurable: true },
    });
    img.dispatchEvent(new Event('load'));
    fixture.detectChanges();
    host.src.set('/qa/second.jpg');
    fixture.detectChanges();
    expect(photo().querySelector('img')!.getAttribute('src')).toBe('/qa/second.jpg');
    expect(photo().querySelector('img')!.style.objectPosition).toBe('50% 28%');
  });

  it('analyzes an already loaded image when automatic framing is enabled later', () => {
    const sample = vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
    host.src.set('/qa/cached-mode-change.jpg');
    fixture.detectChanges();
    const img = photo().querySelector('img')!;
    Object.defineProperties(img, {
      complete: { value: true, configurable: true },
      naturalWidth: { value: 100, configurable: true },
      naturalHeight: { value: 400, configurable: true },
      clientWidth: { value: 200, configurable: true },
      clientHeight: { value: 100, configurable: true },
    });
    host.framing.set('auto');
    fixture.detectChanges();
    expect(sample).toHaveBeenCalled();
    expect(img.style.objectPosition).not.toBe('50% 50%');
    expect(img.style.objectPosition).not.toBe('50% 28%');
  });
});
