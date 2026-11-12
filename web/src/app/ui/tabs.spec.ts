import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { UiTab, UiTabs } from './tabs';

@Component({
  imports: [UiTabs, UiTab],
  template: `
    <ui-tabs>
      <button uiTab value="journey">Journey</button>
      <button uiTab value="care">Care</button>
      <button uiTab value="purchases">Purchases</button>
    </ui-tabs>
  `,
})
class Host {}

@Component({
  imports: [UiTabs, UiTab],
  template: `
    <ui-tabs [selectedValue]="selected()" (selectionChange)="emitted.push($event)">
      <button uiTab value="care">Care</button>
      <button uiTab value="details">Details</button>
      <button uiTab value="purchases">Purchases</button>
    </ui-tabs>
  `,
})
class ControlledHost {
  readonly selected = signal<string | null>(null);
  readonly emitted: string[] = [];
}

function key(el: Element, k: string): void {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
}

describe('UiTabs', () => {
  let root: HTMLElement;

  beforeEach(() => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    root = fixture.nativeElement as HTMLElement;
  });

  it('roving tabindex: exactly one tab is tabbable', () => {
    const tabs = Array.from(root.querySelectorAll<HTMLElement>('[role="tab"]'));
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');
    expect(tabs.map((t) => t.getAttribute('tabindex'))).toEqual(['0', '-1', '-1']);
  });

  it('ArrowRight/ArrowLeft/Home/End move selection and focus', () => {
    const tabs = Array.from(root.querySelectorAll<HTMLElement>('[role="tab"]'));
    key(tabs[0], 'ArrowRight');
    expect(document.activeElement).toBe(tabs[1]);
    expect(tabs[1].getAttribute('aria-selected')).toBe('true');
    expect(tabs[0].getAttribute('tabindex')).toBe('-1');
    key(tabs[1], 'ArrowLeft');
    key(tabs[0], 'ArrowLeft');
    expect(document.activeElement).toBe(tabs[2]);
    key(tabs[2], 'Home');
    expect(document.activeElement).toBe(tabs[0]);
    key(tabs[0], 'End');
    expect(document.activeElement).toBe(tabs[2]);
  });

  it('external selectedValue selects by value silently, then keys behave normally', () => {
    const fixture = TestBed.createComponent(ControlledHost);
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    const tabs = Array.from(root.querySelectorAll<HTMLElement>('[role="tab"]'));
    // uncontrolled start: first tab, exactly as before
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');

    fixture.componentInstance.selected.set('details');
    fixture.detectChanges();
    expect(tabs[1].getAttribute('aria-selected')).toBe('true');
    expect(tabs.map((t) => t.getAttribute('aria-selected'))).toEqual(['false', 'true', 'false']);
    expect(tabs.map((t) => t.getAttribute('tabindex'))).toEqual(['-1', '0', '-1']);
    // the caller asked for this, so nothing comes back out and focus stays put
    expect(fixture.componentInstance.emitted).toEqual([]);
    expect(document.activeElement).not.toBe(tabs[1]);

    key(tabs[1], 'ArrowLeft');
    fixture.detectChanges();
    expect(document.activeElement).toBe(tabs[0]);
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');
    expect(tabs.map((t) => t.getAttribute('tabindex'))).toEqual(['0', '-1', '-1']);
    expect(fixture.componentInstance.emitted).toEqual(['care']);
  });
});
