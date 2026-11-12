import {
  AfterViewInit,
  Component,
  ContentChildren,
  ElementRef,
  EventEmitter,
  Input,
  input,
  Output,
  QueryList,
} from '@angular/core';

@Component({
  selector: '[uiTab]',
  template: '<ng-content />',
  host: { role: 'tab', class: 'chip' },
})
export class UiTab {
  readonly el: HTMLElement;
  value = '';
  selected = false;

  constructor(elementRef: ElementRef<HTMLElement>) {
    this.el = elementRef.nativeElement;
    this.value = this.el.getAttribute('value') ?? this.el.textContent?.trim().toLowerCase() ?? '';
    this.el.setAttribute('id', `tab-${this.value}`);
    this.el.setAttribute('aria-controls', `tabpanel-${this.value}`);
  }
}

/** Roving-tabindex tablist with arrow/Home/End keys (WCAG tabs pattern).
 * Usage:
 *   <ui-tabs>
 *     <button uiTab value="journey">Journey</button>
 *     ...
 *   </ui-tabs>
 */
@Component({
  selector: 'ui-tabs',
  template: `
    <div
      class="row"
      [class.section-tabs]="appearance() === 'sections'"
      role="tablist"
      (keydown)="onKey($event)"
      (click)="onClick($event)"
    >
      <ng-content />
    </div>
  `,
  host: { class: 'ui-tabs' },
  styles: `
    .section-tabs {
      display: grid;
      grid-auto-flow: column;
      grid-auto-columns: minmax(0, 1fr);
      gap: 0;
    }
  `,
})
export class UiTabs implements AfterViewInit {
  readonly appearance = input<'pills' | 'sections'>('pills');
  @ContentChildren(UiTab) private tabs!: QueryList<UiTab>;
  @Output() readonly selectionChange = new EventEmitter<string>();
  private selectedIndex = 0;
  /** the last externally requested value; may arrive before the tabs exist */
  private pendingValue: string | null = null;

  /**
   * Optional external control: selects the tab whose `value` matches, silently —
   * no `selectionChange` emit (the caller already knows) and no focus steal.
   * Left unset, the first tab stays selected as before.
   */
  @Input()
  set selectedValue(value: string | null) {
    this.pendingValue = value;
    this.applyPending();
  }

  ngAfterViewInit(): void {
    this.applyPending();
    this.apply();
  }

  protected onKey(ev: KeyboardEvent): void {
    const list = this.tabs.toArray();
    let next = this.selectedIndex;
    switch (ev.key) {
      case 'ArrowRight':
        next = (next + 1) % list.length;
        break;
      case 'ArrowLeft':
        next = (next - 1 + list.length) % list.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = list.length - 1;
        break;
      default:
        return;
    }
    ev.preventDefault();
    this.select(next, true);
  }

  selectIndex(i: number): void {
    this.select(i, false);
  }

  protected onClick(ev: MouseEvent): void {
    const target = (ev.target as Element).closest('[uiTab]');
    if (!target) return;
    const i = this.tabs.toArray().findIndex((t) => t.el === target);
    if (i >= 0) this.select(i, true);
  }

  /** Applies a pending external selection: match by value, no emit, no focus.
   * An unknown value (or no children yet) leaves the current selection alone. */
  private applyPending(): void {
    if (this.pendingValue === null || !this.tabs) return;
    const i = this.tabs.toArray().findIndex((t) => t.value === this.pendingValue);
    if (i < 0) return;
    this.selectedIndex = i;
    this.apply();
  }

  private select(i: number, focus: boolean): void {
    this.selectedIndex = i;
    this.apply();
    if (focus) this.tabs.toArray()[i]?.el.focus();
    this.selectionChange.emit(this.tabs.toArray()[i]?.value ?? '');
  }

  private apply(): void {
    this.tabs?.toArray().forEach((t, i) => {
      t.selected = i === this.selectedIndex;
      t.el.setAttribute('aria-selected', String(t.selected));
      t.el.setAttribute('tabindex', t.selected ? '0' : '-1');
    });
  }
}
