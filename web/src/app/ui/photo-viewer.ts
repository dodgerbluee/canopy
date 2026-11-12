import { A11yModule } from '@angular/cdk/a11y';
import { Overlay, OverlayRef } from '@angular/cdk/overlay';
import { ComponentPortal } from '@angular/cdk/portal';
import {
  ChangeDetectionStrategy,
  Component,
  ElementRef,
  afterRenderEffect,
  Injectable,
  InjectionToken,
  Injector,
  computed,
  inject,
  signal,
} from '@angular/core';

export interface ViewerPhoto {
  id: string;
  src: string;
  original: string;
  thumbnail?: string;
  alt: string;
  detail?: string;
  details?: () => void;
}
interface ViewerState {
  photos: readonly ViewerPhoto[];
  index: number;
  close: () => void;
}
const VIEWER = new InjectionToken<ViewerState>('PHOTO_VIEWER');

/** Presentation only: callers provide authorized image/download URLs. No
 * canvas re-encoding: Save downloads the original, retaining capture metadata. */
@Injectable({ providedIn: 'root' })
export class UiPhotoViewer {
  private readonly overlay = inject(Overlay);
  private readonly injector = inject(Injector);
  private active: OverlayRef | null = null;

  open(photos: readonly ViewerPhoto[], index = 0): (() => void) | undefined {
    if (!photos.length) return;
    this.close();
    const focus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const overlay = this.overlay.create({
      width: '100%',
      height: '100%',
      positionStrategy: this.overlay.position().global().top('0').left('0'),
      scrollStrategy: this.overlay.scrollStrategies.block(),
      disposeOnNavigation: true,
    });
    this.active = overlay;
    const background = [...document.body.children]
      .filter(
        (element): element is HTMLElement =>
          element instanceof HTMLElement && !element.contains(overlay.overlayElement),
      )
      .map((element) => ({
        element,
        inert: element.inert,
        hidden: element.getAttribute('aria-hidden'),
      }));
    for (const { element } of background) {
      element.inert = true;
      element.setAttribute('aria-hidden', 'true');
    }
    overlay.detachments().subscribe(() => {
      if (this.active === overlay) this.active = null;
      for (const { element, inert, hidden } of background) {
        element.inert = inert;
        if (hidden === null) element.removeAttribute('aria-hidden');
        else element.setAttribute('aria-hidden', hidden);
      }
      if (focus?.isConnected) focus.focus({ preventScroll: true });
    });
    overlay.attach(
      new ComponentPortal(
        PhotoViewer,
        null,
        Injector.create({
          parent: this.injector,
          providers: [
            {
              provide: VIEWER,
              useValue: {
                photos: [...photos],
                index: Math.max(0, Math.min(index, photos.length - 1)),
                close: () => overlay.dispose(),
              } satisfies ViewerState,
            },
          ],
        }),
      ),
    );
    return () => overlay.dispose();
  }
  close(): void {
    this.active?.dispose();
    this.active = null;
  }
}

@Component({
  selector: 'ui-photo-viewer',
  imports: [A11yModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section
      class="viewer"
      role="dialog"
      aria-modal="true"
      aria-label="Photo viewer"
      cdkTrapFocus
      cdkTrapFocusAutoCapture
      tabindex="-1"
      (keydown)="key($event)"
    >
      <header class="viewer-bar">
        <div class="viewer-heading">
          <strong>{{ photo().alt }}</strong>
          <span>{{ index() + 1 }} / {{ data.photos.length }}</span>
        </div>
        @if (photo().details) {
          <button type="button" class="viewer-button" (click)="photo().details?.()">Details</button>
        }
        <a class="viewer-button save" [href]="photo().original" download>Save original</a>
        <button
          class="viewer-button"
          type="button"
          aria-label="Close photo viewer"
          cdkFocusInitial
          (click)="data.close()"
        >
          ✕
        </button>
      </header>
      <div class="viewer-stage" [class.zoomed]="zoomed()">
        @if (failed() === photo().id) {
          <p role="alert">This photo could not be loaded. Try saving the original.</p>
        } @else {
          <img [src]="photo().src" [alt]="photo().alt" (error)="failed.set(photo().id)" />
        }
      </div>
      <footer class="viewer-footer">
        <div class="viewer-controls">
          <button
            type="button"
            class="viewer-button"
            aria-label="Previous photo"
            [disabled]="index() === 0"
            (click)="move(-1)"
          >
            ‹
          </button>
          <p aria-live="polite">{{ photo().detail || photo().alt }}</p>
          <button
            type="button"
            class="viewer-button"
            [attr.aria-pressed]="zoomed()"
            (click)="zoomed.set(!zoomed())"
          >
            {{ zoomed() ? 'Fit' : 'Zoom' }}
          </button>
          <button
            type="button"
            class="viewer-button"
            aria-label="Next photo"
            [disabled]="index() === data.photos.length - 1"
            (click)="move(1)"
          >
            ›
          </button>
        </div>
        @if (data.photos.length > 1) {
          <nav class="viewer-strip" aria-label="Photo thumbnails">
            @for (item of data.photos; track item.id; let i = $index) {
              <button
                type="button"
                [attr.aria-label]="'View photo ' + (i + 1)"
                [attr.aria-current]="index() === i ? 'true' : null"
                (click)="select(i)"
              >
                <img [src]="item.thumbnail || item.src" alt="" loading="lazy" />
              </button>
            }
          </nav>
        }
      </footer>
    </section>
  `,
  styleUrl: './photo-viewer.css',
})
export class PhotoViewer {
  private readonly element = inject<ElementRef<HTMLElement>>(ElementRef);
  constructor() {
    afterRenderEffect(() => {
      const index = this.index();
      this.element.nativeElement
        .querySelector<HTMLElement>(`.viewer-strip button:nth-child(${index + 1})`)
        ?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
    });
  }
  protected readonly data = inject(VIEWER);
  protected readonly index = signal(this.data.index);
  protected readonly photo = computed(() => this.data.photos[this.index()]);
  protected readonly zoomed = signal(false);
  protected readonly failed = signal<string | null>(null);
  protected select(index: number): void {
    this.index.set(index);
    this.zoomed.set(false);
  }
  protected move(offset: number): void {
    const next = this.index() + offset;
    if (next >= 0 && next < this.data.photos.length) this.select(next);
  }
  protected key(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      this.data.close();
    }
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
      event.preventDefault();
      this.move(event.key === 'ArrowLeft' ? -1 : 1);
    }
  }
}
