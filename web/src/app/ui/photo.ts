import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  ElementRef,
  computed,
  effect,
  inject,
  input,
  signal,
  viewChild,
} from '@angular/core';
import { UiPhotoViewer } from './photo-viewer';
import { FALLBACK_FOCUS, findPlantFocus, photoPosition, type PlantFocus } from './photo-framing';

/** Sprout glyph (same 24×24 / 1.65-stroke language as UiIcon) drawn locally so
 * an absent photo reads as an intentional, on-brand state — never a broken
 * image and never invented imagery. */
const PLACEHOLDER_PATH = 'M12 21v-9M12 15C4 16 3 6 3 6s10-1 9 9Zm0-4C12 3 21 3 21 3s1 9-9 9';

/** Bounded analysis cache: focus per URL, never bytes/canvas/elements. */
const FOCUS_CACHE_LIMIT = 64;
const focusCache = new Map<string, { focus: PlantFocus; width: number; height: number }>();

/** Bounded sample canvas — at most this many pixels on either axis. */
const SAMPLE_EDGE = 96;

/**
 * Pure photo primitive: shows the real uploaded image when there is one,
 * otherwise a local gradient/botanical placeholder with a useful label.
 * A failed load falls back once and does not re-request; changing `src`
 * resets that decision.
 *
 * `framing` decides how a photo that does not match the box is cropped:
 * - `center` (default, unchanged legacy behaviour): centred `cover`.
 * - `full`: the whole image stays visible (`contain`) — archives and
 *   documentary photos keep their composition.
 * - `auto`: `cover`, but the crop window is placed by a bounded local sample
 *   of the loaded pixels, so plant cards and heroes favour canopy/foliage
 *   instead of a trunk. No network calls, no AI provider, nothing persisted
 *   about the original file.
 */
@Component({
  selector: 'ui-photo',
  changeDetection: ChangeDetectionStrategy.OnPush,
  host: {
    // Callers style the box; these two inputs make the primitive answer to
    // `ratio` (thumbnail grids) and `compact` (label kept, not painted).
    '[attr.data-compact]': 'compact() ? "" : null',
    '[style.--photo-ratio]': 'ratio() || null',
    '[attr.role]': 'expandable() && shown() ? "button" : null',
    '[attr.tabindex]': 'expandable() && shown() ? 0 : null',
    '[attr.aria-label]': 'expandable() && shown() ? "View " + imageAlt() + " fullscreen" : null',
    '[attr.aria-haspopup]': 'expandable() && shown() ? "dialog" : null',
    '[style.cursor]': 'expandable() && shown() ? "zoom-in" : null',
    '(click)': 'expand($event)',
    '(keydown)': 'expandKey($event)',
  },
  template: `
    @if (shown(); as src) {
      <img
        #img
        [src]="src"
        [alt]="imageAlt()"
        decoding="async"
        [style.object-fit]="fitCss()"
        [style.object-position]="positionCss()"
        (load)="onImgLoad(img)"
        (error)="onError()"
      />
    } @else {
      <div class="ph">
        <svg
          class="ph-glyph"
          viewBox="0 0 24 24"
          stroke-width="1.4"
          aria-hidden="true"
          focusable="false"
        >
          <path [attr.d]="PLACEHOLDER_PATH" />
        </svg>
        <span class="ph-label" [class.visually-only]="compact()">{{ placeholderLabel() }}</span>
      </div>
    }
  `,
  styles: `
    :host {
      display: block;
      position: relative;
      overflow: hidden;
      aspect-ratio: var(--photo-ratio, auto);
      background: var(--surface-raised);
    }
    img {
      display: block;
      width: 100%;
      height: 100%;
      object-fit: cover;
    }
    .ph {
      height: 100%;
      min-height: 88px;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      gap: var(--sp-2);
      padding: var(--sp-3);
      text-align: center;
      background:
        radial-gradient(
          120% 95% at 12% 0%,
          color-mix(in srgb, var(--accent) 18%, transparent),
          transparent 62%
        ),
        linear-gradient(
          160deg,
          var(--surface-raised),
          color-mix(in srgb, var(--surface-raised) 55%, var(--accent) 12%)
        );
      border: 1px dashed var(--border);
      color: var(--text-muted);
    }
    .ph-glyph {
      width: 34px;
      height: 34px;
      flex: none;
      fill: none;
      stroke: var(--accent);
      stroke-linecap: round;
      stroke-linejoin: round;
      opacity: 0.8;
    }
    .ph-label {
      font-size: var(--fs-caption);
      line-height: var(--lh-caption);
      font-weight: 600;
      max-width: 24ch;
    }
  `,
})
export class UiPhoto {
  /** real image URL, or null/undefined when the record has no photo yet */
  readonly src = input<string | null | undefined>(null);
  readonly expandable = input(false);
  readonly originalSrc = input<string | null>(null);
  readonly previewSrc = input<string | null>(null);
  private readonly viewer = inject(UiPhotoViewer);
  private closeViewer?: () => void;
  protected expand(event: Event): void {
    const src = this.shown();
    if (!this.expandable() || !src) return;
    event.preventDefault();
    event.stopPropagation();
    this.closeViewer = this.viewer.open([
      {
        id: src,
        src: this.previewSrc() ?? src,
        original: this.originalSrc() ?? src,
        alt: this.imageAlt(),
      },
    ]);
  }
  protected expandKey(event: KeyboardEvent): void {
    if (event.key === 'Enter' || event.key === ' ') this.expand(event);
  }
  readonly alt = input('');
  /** what the photo is of ("plant", "receipt", "journal") — shapes both labels */
  readonly category = input('');
  /** label stays in the accessible name but is not painted (thumbnails) */
  readonly compact = input(false);
  readonly ratio = input('');
  /**
   * `center` keeps the historical centred cover crop (compatibility default);
   * `full` shows the entire image; `auto` frames the crop on the plant.
   */
  readonly framing = input<'auto' | 'full' | 'center'>('center');

  /** the URL we already tried and failed to load; a new src resets this */
  private readonly failedSrc = signal<string | null>(null);

  /**
   * Framing state for the URL currently in the DOM. A new src clears it, so a
   * late async result can never be applied to a different photo.
   */
  private readonly analyzedSrc = signal<string | null>(null);
  private readonly focus = signal<PlantFocus>(FALLBACK_FOCUS);
  private readonly naturalSize = signal<{ w: number; h: number } | null>(null);
  /**
   * Host box in CSS pixels, kept outside the signal graph on purpose: the box
   * belongs to the layout rather than to the image, so it stays valid while a
   * new src's focus/size are still arriving, and the only signal the resize
   * path writes is `position` — guarded against destruction.
   */
  private boxSize: { w: number; h: number } | null = null;

  /** the element we attached the ResizeObserver to, and its live src */
  private observedImg: HTMLImageElement | null = null;
  private observedSrc: string | null = null;
  private observer: ResizeObserver | null = null;

  private readonly destroyRef = inject(DestroyRef);

  /** the img element Angular is currently rendering, when there is one.
   * A template element query returns ElementRef, not the element itself. */
  private readonly imgEl = viewChild<ElementRef<HTMLImageElement>>('img');

  protected readonly PLACEHOLDER_PATH = PLACEHOLDER_PATH;

  constructor() {
    // src/framing changes: drop framing state that belongs to the old URL, and
    // re-observe the element that is actually in the DOM now. A cached-complete
    // image never fires `load`, so this is also where its box gets measured.
    effect(() => {
      const src = this.src();
      const framing = this.framing();
      this.resetFramingState(src);
      this.syncObserver(src, framing);
      this.recomputePosition();
    });

    this.destroyRef.onDestroy(() => {
      this.detachObserver();
      this.closeViewer?.();
    });
  }

  /** CSS object-fit for the current framing mode. */
  protected readonly fitCss = computed(() => (this.framing() === 'full' ? 'contain' : 'cover'));

  /** CSS object-position: fixed for center/full, geometry-driven for auto. */
  protected readonly position = signal<string | null>(null);

  protected readonly positionCss = computed(() => {
    if (this.framing() !== 'auto') return '50% 50%';
    return this.position() ?? '50% 28%';
  });

  protected readonly shown = computed(() => {
    const src = this.src();
    if (!src) return null;
    return this.failedSrc() === src ? null : src;
  });

  protected readonly imageAlt = computed(() => {
    const alt = this.alt();
    if (alt) return alt;
    const category = this.category();
    return category ? `${category} photo` : 'Photo';
  });

  protected readonly placeholderLabel = computed(() => {
    // only the URL we are actually showing (and already failed) is a load error;
    // a cleared src is the plain "nothing here yet" state again
    const src = this.src();
    if (src && this.failedSrc() === src) return 'Photo could not be loaded';
    const category = this.category();
    return category ? `No ${category} photo yet` : 'No photo yet';
  });

  protected onError(): void {
    const src = this.src();
    if (src) this.failedSrc.set(src);
  }

  /**
   * Load handler for the element Angular just created. Runs for both fresh
   * loads and cached-complete images that fire `load` again, and it is where
   * the one bounded pixel sample happens (auto framing only).
   */
  protected onImgLoad(img: HTMLImageElement): void {
    if (this.destroyRef.destroyed) return;
    const src = this.src();
    // stale element/event: only act when this element shows the URL we expect
    if (!src || img.getAttribute('src') !== src) return;
    // center/full need no observers and no pixel sampling: fixed position CSS
    // already does the job.
    if (this.framing() !== 'auto') return;
    this.observeBox(img, src);
    this.readNaturalSize(img, src);
    this.ensureFocus(img, src);
  }

  private resetFramingState(src: string | null | undefined): void {
    const next = src ?? null;
    if (this.analyzedSrc() === next) return;
    this.analyzedSrc.set(next);
    this.focus.set(FALLBACK_FOCUS);
    this.naturalSize.set(null);
    this.position.set(null);
  }

  /**
   * Recomputes object-position from cached focus + natural size + measured box.
   * Called after load, after analysis, after a resize and after a src/framing
   * change — never re-samples pixels.
   */
  private recomputePosition(): void {
    if (this.destroyRef.destroyed) return;
    if (this.framing() !== 'auto') return;
    const box = this.boxSize;
    const size = this.naturalSize();
    if (!box || !size) return; // nothing to place until both are known
    this.position.set(photoPosition(this.focus(), size.w, size.h, box.w, box.h));
  }

  /** natural size of the loaded element, oriented as the browser renders it. */
  private readNaturalSize(img: HTMLImageElement, src: string): void {
    const cached = focusCache.get(src);
    if (cached && cached.width > 0 && cached.height > 0) {
      this.setNaturalSize(cached.width, cached.height);
      return;
    }
    const w = img.naturalWidth;
    const h = img.naturalHeight;
    if (w > 0 && h > 0) this.setNaturalSize(w, h);
  }

  private setNaturalSize(w: number, h: number): void {
    if (this.destroyRef.destroyed) return;
    const prev = this.naturalSize();
    if (prev && prev.w === w && prev.h === h) return;
    this.naturalSize.set({ w, h });
    this.recomputePosition();
  }

  /** Sample the already-loaded pixels once per URL into the bounded cache. */
  private ensureFocus(img: HTMLImageElement, src: string): void {
    const cached = focusCache.get(src);
    if (cached) {
      this.applyFocus(src, cached.focus, cached.width, cached.height);
      return;
    }

    const w = img.naturalWidth;
    const h = img.naturalHeight;
    if (!(w > 0) || !(h > 0)) {
      // nothing decoded to sample: portrait upper-canopy fallback, no throw
      this.applyFocus(src, FALLBACK_FOCUS, 0, 0);
      return;
    }

    let result: { focus: PlantFocus; width: number; height: number };
    try {
      const scale = Math.min(1, SAMPLE_EDGE / Math.max(w, h));
      const sw = Math.max(1, Math.round(w * scale));
      const sh = Math.max(1, Math.round(h * scale));
      const canvas = document.createElement('canvas');
      canvas.width = sw;
      canvas.height = sh;
      const ctx = canvas.getContext('2d');
      if (!ctx || typeof ctx.getImageData !== 'function') throw new Error('no 2d context');
      ctx.drawImage(img, 0, 0, sw, sh);
      // the canvas is never attached anywhere and is dropped as soon as the
      // sample is read: only the focus number is retained
      const data = ctx.getImageData(0, 0, sw, sh).data;
      result = { focus: findPlantFocus(data, sw, sh), width: w, height: h };
    } catch {
      // tainted/cross-origin/unsupported context/anything odd: honest fallback
      result = { focus: FALLBACK_FOCUS, width: w, height: h };
    }

    if (focusCache.size >= FOCUS_CACHE_LIMIT) {
      const oldest = focusCache.keys().next();
      if (!oldest.done) focusCache.delete(oldest.value);
    }
    focusCache.set(src, result);
    this.applyFocus(src, result.focus, result.width, result.height);
  }

  private applyFocus(src: string, focus: PlantFocus, width: number, height: number): void {
    if (this.destroyRef.destroyed) return;
    // guard: never write framing for a URL we are no longer showing
    if (this.src() !== src || this.failedSrc() === src) return;
    this.focus.set(focus);
    if (width > 0 && height > 0) this.setNaturalSize(width, height);
    this.recomputePosition();
  }

  /** (Re)attach the ResizeObserver to the img element currently in the DOM. */
  private syncObserver(src: string | null | undefined, framing: 'auto' | 'full' | 'center'): void {
    if (framing !== 'auto' || !src) {
      this.detachObserver();
      return;
    }
    const img = this.imgEl()?.nativeElement ?? null;
    if (!img) {
      // the old element is gone: drop its observer so a stale one never fires
      this.detachObserver();
      return;
    }
    if (this.observedImg === img && this.observedSrc === src) return;
    // A cached-complete image never fires `load`, and framing can flip to
    // auto after the image already loaded. If this element already shows the
    // current URL and decoded, treat it as loaded right here. onImgLoad
    // observes directly and never calls back into syncObserver, so there is
    // no recursion risk.
    if (img.getAttribute('src') === src && img.complete && img.naturalWidth > 0) {
      this.onImgLoad(img);
      return;
    }
    this.observeBox(img, src);
  }

  private observeBox(img: HTMLImageElement, src: string): void {
    if (this.observedImg === img && this.observedSrc === src) return;
    this.detachObserver();
    const factory = (globalThis as { ResizeObserver?: typeof ResizeObserver }).ResizeObserver;
    if (typeof factory !== 'function') {
      // no resize signal available: measure once and accept a static box
      this.measureBox(img);
      return;
    }
    this.observer = new factory(() => this.measureBox(img));
    this.observer.observe(img);
    this.observedImg = img;
    this.observedSrc = src;
    this.measureBox(img);
  }

  private measureBox(img: HTMLImageElement): void {
    if (this.destroyRef.destroyed) return;
    const w = img.clientWidth;
    const h = img.clientHeight;
    if (!(w > 0) || !(h > 0)) return;
    const prev = this.boxSize;
    if (prev && prev.w === w && prev.h === h) return;
    this.boxSize = { w, h };
    this.recomputePosition();
  }

  private detachObserver(): void {
    this.observer?.disconnect();
    this.observer = null;
    this.observedImg = null;
    this.observedSrc = null;
  }
}
