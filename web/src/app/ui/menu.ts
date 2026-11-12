import {
  afterNextRender,
  Component,
  ContentChildren,
  ElementRef,
  EventEmitter,
  HostListener,
  Injector,
  Output,
  QueryList,
  ViewChild,
  inject,
  input,
} from '@angular/core';

let nextMenuId = 0;

@Component({
  selector: '[uiMenuItem]',
  template: '<ng-content />',
  host: { role: 'menuitem', class: 'menu-item', tabindex: '0' },
})
export class UiMenuItem {
  readonly el: HTMLElement;
  @Output() readonly triggered = new EventEmitter<void>();

  constructor(elementRef: ElementRef<HTMLElement>) {
    this.el = elementRef.nativeElement;
    this.el.addEventListener('click', () => this.triggered.emit());
  }
}

/** Keyboard-navigable menu button + popup (role=menu semantics).
 * Usage:
 *   <ui-menu label="Add">
 *     <button uiMenuItem (triggered)="...">New plant</button>
 *   </ui-menu>
 */
@Component({
  selector: 'ui-menu',
  template: `
    <button
      #trigger
      type="button"
      class="btn"
      [attr.aria-haspopup]="'menu'"
      [attr.aria-expanded]="open"
      [attr.aria-controls]="open ? panelId : null"
      (click)="toggle()"
      (keydown)="onTriggerKey($event)"
    >
      {{ label() }}
    </button>
    @if (open) {
      <div
        class="menu-panel"
        role="menu"
        [attr.id]="panelId"
        [attr.aria-label]="label()"
        (keydown)="onMenuKey($event)"
        (click)="onMenuClick($event)"
      >
        <ng-content />
      </div>
    }
  `,
  host: { class: 'ui-menu' },
})
export class UiMenu {
  readonly label = input('Menu');
  protected open = false;
  protected readonly panelId = `ui-menu-${++nextMenuId}`;

  @ViewChild('trigger') private triggerRef?: ElementRef<HTMLButtonElement>;
  private get triggerEl(): HTMLButtonElement | undefined {
    return this.triggerRef?.nativeElement;
  }
  @ContentChildren(UiMenuItem) private items!: QueryList<UiMenuItem>;

  private readonly hostEl: HTMLElement;
  private readonly injector = inject(Injector);

  constructor(elementRef: ElementRef<HTMLElement>) {
    this.hostEl = elementRef.nativeElement;
  }

  @HostListener('document:keydown.escape')
  protected onEscape(): void {
    if (this.open) this.close();
  }

  @HostListener('document:pointerdown', ['$event'])
  protected onDocPointerDown(ev: PointerEvent): void {
    if (this.open && !this.hostEl.contains(ev.target as Node)) this.close(false);
  }

  @HostListener('document:focusin', ['$event'])
  protected onDocFocusIn(ev: FocusEvent): void {
    if (this.open && !this.hostEl.contains(ev.target as Node)) this.close(false);
  }

  protected toggle(): void {
    this.open = !this.open;
    if (this.open) this.scheduleFocus(0);
  }

  protected close(refocus = true): void {
    this.open = false;
    if (refocus) this.triggerEl?.focus();
  }

  protected onTriggerKey(ev: KeyboardEvent): void {
    if (ev.key === 'ArrowDown') {
      ev.preventDefault();
      this.open = true;
      this.scheduleFocus(0);
    } else if (ev.key === 'ArrowUp') {
      ev.preventDefault();
      this.open = true;
      this.scheduleFocus(this.items.length - 1);
    } else if (ev.key === 'Enter' || ev.key === ' ') {
      ev.preventDefault();
      this.open = true;
      this.scheduleFocus(0);
    }
  }

  protected onMenuClick(ev: MouseEvent): void {
    if (!(ev.target instanceof Element)) return;
    const item = ev.target.closest('[role="menuitem"]');
    if (!item) return;
    if (item.hasAttribute('disabled') || item.getAttribute('aria-disabled') === 'true') return;
    this.close();
  }

  protected onMenuKey(ev: KeyboardEvent): void {
    const list = this.items.toArray();
    if (!list.length) return;
    const idx = list.findIndex((i) => i.el === document.activeElement);
    switch (ev.key) {
      case 'ArrowDown':
        ev.preventDefault();
        this.focusItem((idx + 1) % list.length);
        break;
      case 'ArrowUp':
        ev.preventDefault();
        this.focusItem((idx - 1 + list.length) % list.length);
        break;
      case 'Home':
        ev.preventDefault();
        this.focusItem(0);
        break;
      case 'End':
        ev.preventDefault();
        this.focusItem(list.length - 1);
        break;
      case 'Escape':
        ev.preventDefault();
        this.close();
        break;
      case 'Tab':
        this.close(false);
        break;
    }
  }

  private scheduleFocus(index: number): void {
    afterNextRender(
      {
        write: () => {
          if (this.open && this.hostEl.isConnected) this.focusItem(index);
        },
      },
      { injector: this.injector },
    );
  }

  private focusItem(i: number): void {
    this.items.toArray()[i]?.el.focus();
  }
}
