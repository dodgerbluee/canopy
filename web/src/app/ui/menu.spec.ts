import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { UiMenu, UiMenuItem } from './menu';

@Component({
  imports: [UiMenu, UiMenuItem],
  template: `
    <ui-menu label="Add">
      <button uiMenuItem (triggered)="log('plant')">New plant</button>
      <button uiMenuItem (triggered)="log('photo')">Add photos</button>
      <button uiMenuItem (triggered)="log('log')">Quick log</button>
    </ui-menu>
  `,
})
class Host {
  readonly events: string[] = [];
  log(v: string): void {
    this.events.push(v);
  }
}

function key(el: Element, k: string): void {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
}

/** let the component's queueMicrotask focus step run */
async function settle(): Promise<void> {
  await new Promise((res) => setTimeout(res, 0));
}

describe('UiMenu', () => {
  let fixture: ComponentFixture<Host>;
  let root: HTMLElement;

  beforeEach(() => {
    fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    root = fixture.nativeElement as HTMLElement;
  });

  it('trigger exposes menu state', () => {
    const trigger = root.querySelector('button[aria-haspopup="menu"]')!;
    expect(trigger.getAttribute('aria-expanded')).toBe('false');
    trigger.dispatchEvent(new MouseEvent('click'));
    fixture.detectChanges();
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(root.querySelectorAll('[role="menuitem"]').length).toBe(3);
  });

  it('ArrowDown opens and cycles focus; click activates item', async () => {
    const trigger = root.querySelector('button[aria-haspopup="menu"]')! as HTMLButtonElement;
    key(trigger, 'ArrowDown');
    fixture.detectChanges();
    await settle();
    const items = Array.from(root.querySelectorAll<HTMLElement>('[role="menuitem"]'));
    expect(document.activeElement).toBe(items[0]);
    key(items[0], 'ArrowDown');
    expect(document.activeElement).toBe(items[1]);
    key(items[1], 'ArrowUp');
    expect(document.activeElement).toBe(items[0]);
    key(items[0], 'End');
    expect(document.activeElement).toBe(items[2]);
    items[1].dispatchEvent(new MouseEvent('click'));
    expect(fixture.componentInstance.events).toEqual(['photo']);
  });

  it('Escape closes and refocuses the trigger', () => {
    const trigger = root.querySelector('button[aria-haspopup="menu"]')! as HTMLButtonElement;
    trigger.dispatchEvent(new MouseEvent('click'));
    fixture.detectChanges();
    const panel = root.querySelector('[role="menu"]')!;
    key(panel, 'Escape');
    fixture.detectChanges();
    expect(root.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
  it('closes after selection, invokes the action once, and restores trigger focus', async () => {
    const trigger = root.querySelector<HTMLButtonElement>('button[aria-haspopup=menu]')!;
    trigger.click();
    fixture.detectChanges();
    await settle();
    root.querySelectorAll<HTMLButtonElement>('[role=menuitem]')[1].click();
    fixture.detectChanges();
    expect(fixture.componentInstance.events).toEqual(['photo']);
    expect(root.querySelector('[role=menu]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(trigger.hasAttribute('aria-controls')).toBe(false);
  });
  it('dismisses on outside pointerdown without stealing outside focus', async () => {
    const trigger = root.querySelector<HTMLButtonElement>('button[aria-haspopup=menu]')!;
    trigger.click();
    fixture.detectChanges();
    await settle();
    const outside = document.createElement('button');
    outside.textContent = 'Outside';
    document.body.append(outside);
    try {
      outside.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
      outside.focus();
      fixture.detectChanges();
      expect(root.querySelector('[role=menu]')).toBeNull();
      expect(document.activeElement).toBe(outside);
    } finally {
      outside.remove();
    }
  });
  it('ArrowUp opens on the last item and Tab dismisses without cancelling native navigation', async () => {
    const trigger = root.querySelector<HTMLButtonElement>('button[aria-haspopup=menu]')!;
    key(trigger, 'ArrowUp');
    fixture.detectChanges();
    await settle();
    const items = root.querySelectorAll<HTMLElement>('[role=menuitem]');
    expect(document.activeElement).toBe(items[2]);
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    items[2].dispatchEvent(tab);
    fixture.detectChanges();
    expect(tab.defaultPrevented).toBe(false);
    expect(root.querySelector('[role=menu]')).toBeNull();
  });
  it('closes when focus moves outside the menu', async () => {
    const trigger = root.querySelector<HTMLButtonElement>('button[aria-haspopup=menu]')!;
    trigger.click();
    fixture.detectChanges();
    await settle();
    const outside = document.createElement('button');
    document.body.append(outside);
    try {
      outside.focus();
      fixture.detectChanges();
      expect(root.querySelector('[role=menu]')).toBeNull();
      expect(document.activeElement).toBe(outside);
    } finally {
      outside.remove();
    }
  });
});
