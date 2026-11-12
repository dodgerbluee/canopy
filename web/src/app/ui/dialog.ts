import {
  Component,
  InjectionToken,
  Injectable,
  Injector,
  OnDestroy,
  TemplateRef,
  ViewContainerRef,
  ViewEncapsulation,
  inject,
} from '@angular/core';
import { A11yModule } from '@angular/cdk/a11y';
import { Overlay, OverlayConfig, OverlayModule, OverlayRef } from '@angular/cdk/overlay';
import { ComponentPortal, PortalModule, TemplatePortal } from '@angular/cdk/portal';
import { Subject } from 'rxjs';
import { firstFocusable, focusables } from './a11y';
import { UiIcon } from './icon';
import { nativePickerIsOpen } from './select';

export type DialogSize = 'compact' | 'regular' | 'wide';

/** Static text, or a getter (e.g. a signal input) so reactive hosts can update
 * the shell header without reopening the portal. Plain strings behave exactly
 * as before. */
export type DialogText = string | (() => string);

export interface DialogOptions {
  title: DialogText;
  /** Optional supporting copy under the title; wired via aria-describedby. */
  description?: DialogText;
  /** Max width band of the shared shell (regular is the legacy default). */
  size?: DialogSize;
  /** Escape closes only when safe: dialogs with uncommitted work opt out
   * and close through their own confirm flow. */
  closeOnEscape?: boolean;
  /** Routes every USER dismissal (Escape, backdrop, header Close) through a
   * guard instead of closing directly. Guards decide whether/when to close. */
  closeRequest?: () => void;
  /** Legacy default true. Entry dialogs opt out so drafts survive route
   * teardown; the owning component disposes the overlay when it is destroyed. */
  disposeOnNavigation?: boolean;
}

export interface DialogRef<R = unknown> {
  close(result?: R): void;
  readonly closed: Subject<R | undefined>;
  /** The shell panel element, so guarded hosts can track their own content. */
  readonly panel: HTMLElement;
}

interface DialogState {
  opts: DialogOptions;
  content: TemplateRef<unknown>;
  overlayRef: OverlayRef;
}

const DIALOG_STATE = new InjectionToken<DialogState>('DIALOG_STATE');
let nextId = 1;

function resolveText(value: DialogText): string {
  return typeof value === 'function' ? value() : value;
}

/** Initial focus target inside a dialog body: an explicit cdkFocusInitial
 * wins, then the first enabled visible text-entry control, then the body's
 * first focusable. Returns null when the body has nothing focusable. */
function pickInitialFocus(body: ParentNode | null): HTMLElement | null {
  if (!body) return null;
  const explicit = body.querySelector<HTMLElement>('[cdkFocusInitial]');
  if (explicit) return explicit;
  const visible = (el: HTMLElement): boolean =>
    !el.hasAttribute('hidden') &&
    el.getAttribute('aria-hidden') !== 'true' &&
    el.getClientRects().length > 0;
  const entry = focusables(body).find(
    (el) =>
      visible(el) &&
      (el.matches(
        'input:not([type="checkbox"]):not([type="radio"]):not([type="button"]):not([type="submit"]):not([type="reset"])',
      ) ||
        el.matches('select, textarea')),
  );
  return entry ?? firstFocusable(body);
}

/** Focus-trapped, accessible-named modal dialog on CDK mechanics.
 * Shared shell: fixed header (title/description + 44px Close) over a
 * contained-scrolling body. */
@Component({
  selector: 'ui-dialog-shell',
  imports: [OverlayModule, A11yModule, PortalModule, UiIcon],
  encapsulation: ViewEncapsulation.None,
  template: `
    <div
      #panel
      class="dialog-panel"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      cdkTrapFocus
      cdkTrapFocusAutoCapture
      [attr.aria-labelledby]="titleId"
      [attr.aria-describedby]="descriptionText() ? descId : null"
      (keydown.escape)="onEscape($event)"
    >
      <div class="dialog-header">
        <div class="dialog-heading">
          <h2 class="dialog-title" [id]="titleId">{{ titleText() }}</h2>
          @if (descriptionText()) {
            <p class="dialog-desc" [id]="descId">{{ descriptionText() }}</p>
          }
        </div>
        <button
          type="button"
          class="icon-button dialog-close"
          aria-label="Close"
          (click)="requestDismiss()"
        >
          <ui-icon name="close" [size]="20" />
        </button>
      </div>
      <div class="dialog-body">
        <ng-template [cdkPortalOutlet]="contentPortal"></ng-template>
      </div>
    </div>
  `,
})
export class UiDialogShell<R = unknown> implements OnDestroy {
  private readonly overlayRef: OverlayRef;
  readonly opts: DialogOptions;
  readonly contentPortal: TemplatePortal;
  readonly titleId = `dialog-title-${nextId++}`;
  readonly descId = `dialog-desc-${nextId++}`;
  private readonly closed$ = new Subject<R | undefined>();
  readonly closed = this.closed$;
  /** close()/route teardown/dispose must complete closed exactly once */
  private settled = false;

  constructor() {
    const state = inject(DIALOG_STATE);
    this.overlayRef = state.overlayRef;
    this.opts = state.opts;
    this.contentPortal = new TemplatePortal(state.content, inject(ViewContainerRef));
  }

  titleText(): string {
    return resolveText(this.opts.title);
  }

  descriptionText(): string {
    return this.opts.description ? resolveText(this.opts.description) : '';
  }

  onEscape(event: Event): void {
    // A native base-select popup swallows Escape itself: stop here so the
    // dialog stays open while the UA closes the picker.
    if (nativePickerIsOpen(event)) {
      event.stopPropagation();
      return;
    }
    this.requestDismiss();
  }

  /** Every user dismissal path (Escape, backdrop, header Close) funnels here.
   * Guarded dialogs route to their closeRequest; simple dialogs keep the
   * legacy direct close gated by closeOnEscape. */
  requestDismiss(): void {
    if (this.opts.closeRequest) {
      this.opts.closeRequest();
      return;
    }
    if (this.opts.closeOnEscape !== false) this.close();
  }

  /** Programmatic close (owner-confirmed). Idempotent. */
  close(result?: R): void {
    if (this.settled) return;
    this.settled = true;
    this.closed$.next(result);
    this.closed$.complete();
    this.overlayRef.dispose();
  }

  ngOnDestroy(): void {
    // disposeOnNavigation or external overlay teardown: settle exactly once.
    if (this.settled) return;
    this.settled = true;
    this.closed$.next(undefined);
    this.closed$.complete();
  }
}

@Injectable({ providedIn: 'root' })
export class UiDialog {
  private readonly overlay = inject(Overlay);
  private readonly parentInjector = inject(Injector);

  open<R = unknown>(content: TemplateRef<unknown>, opts: DialogOptions): DialogRef<R> {
    const prevFocus = document.activeElement as HTMLElement | null;
    const cfg = new OverlayConfig({
      positionStrategy: this.overlay.position().global().centerHorizontally().centerVertically(),
      hasBackdrop: true,
      backdropClass: 'dialog-backdrop',
      panelClass: ['ui-dialog-host', `ui-dialog-host--${opts.size ?? 'regular'}`],
      scrollStrategy: this.overlay.scrollStrategies.block(),
      disposeOnNavigation: opts.disposeOnNavigation !== false,
    });
    const overlayRef = this.overlay.create(cfg);
    const injector = Injector.create({
      providers: [{ provide: DIALOG_STATE, useValue: { opts, content, overlayRef } }],
      parent: this.parentInjector,
    });
    const cmpRef = overlayRef.attach(new ComponentPortal(UiDialogShell, null, injector));
    const shell = cmpRef.instance as UiDialogShell<R>;
    // zoneless: attach() from an async path (registry.canLeave) is not inside a render pass,
    // so force one initial change detection before reading the DOM (title/content bind here)
    cmpRef.changeDetectorRef.detectChanges();
    // move focus inside synchronously (portal content exists at attach time).
    // Pick the initial target inside the body — an existing cdkFocusInitial
    // wins, then the first enabled visible input/select/textarea, then the
    // body's first focusable — and pin it with cdkFocusInitial BEFORE the
    // trap's auto-capture runs, so the trap never pulls focus back to the
    // header Close. The panel itself stays the fallback.
    const panel = cmpRef.location.nativeElement.querySelector('.dialog-panel') as HTMLElement;
    const body = panel.querySelector('.dialog-body');
    const initial = pickInitialFocus(body);
    initial?.setAttribute('cdkFocusInitial', '');
    (initial ?? firstFocusable(panel) ?? panel).focus();
    // restore focus only to a still-connected original trigger
    shell.closed.subscribe(() => {
      if (prevFocus && prevFocus.isConnected) prevFocus.focus();
    });
    overlayRef.backdropClick().subscribe(() => shell.requestDismiss());
    return {
      close: (result?: R) => shell.close(result),
      closed: shell.closed,
      panel,
    };
  }
}
