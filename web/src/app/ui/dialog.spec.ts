import { vi } from 'vitest';
import { Component, ElementRef, TemplateRef, viewChild } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { UiDialog } from './dialog';

@Component({
  template: `
    <button #trigger>Open</button>
    <ng-template #tpl>
      <p>Discard unsaved work?</p>
      <select id="native-choice">
        <option value="one">One</option>
        <option value="two">Two</option>
      </select>
      <button id="a">Keep editing</button>
      <button id="b">Discard</button>
    </ng-template>
  `,
})
class Host {
  readonly tpl = viewChild.required<TemplateRef<unknown>>('tpl');
  readonly triggerEl = viewChild.required<ElementRef<HTMLButtonElement>>('trigger');
  trigger(): HTMLButtonElement {
    return this.triggerEl().nativeElement;
  }
}

/** zoneless: overlay components tick on the app scheduler (microtask) */
async function settle(): Promise<void> {
  await new Promise((res) => setTimeout(res, 0));
}

describe('UiDialog', () => {
  let host: Host;

  beforeEach(() => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    host = fixture.componentInstance;
  });

  afterEach(() => (document.body.innerHTML = ''));

  it('is modal, named by its title, and moves focus inside', async () => {
    TestBed.inject(UiDialog).open(host.tpl(), { title: 'Discard draft?' });
    await settle();
    const panel = document.querySelector('.dialog-panel')!;
    expect(panel.getAttribute('role')).toBe('dialog');
    expect(panel.getAttribute('aria-modal')).toBe('true');
    const labelId = panel.getAttribute('aria-labelledby')!;
    expect(document.getElementById(labelId)?.textContent).toBe('Discard draft?');
    // auto-capture: focus must land inside the panel
    expect(panel.contains(document.activeElement)).toBe(true);
  });

  it('Escape closes when safe and restores focus to the trigger', async () => {
    host.trigger().focus();
    expect(document.activeElement).toBe(host.trigger());
    TestBed.inject(UiDialog).open(host.tpl(), { title: 'Discard draft?' });
    await settle();
    const panel = document.querySelector('.dialog-panel') as HTMLElement;
    panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(document.querySelector('.dialog-panel')).toBeNull();
    expect(document.activeElement).toBe(host.trigger());
  });

  it('closes an open native picker before dismissing the parent dialog', async () => {
    TestBed.inject(UiDialog).open(host.tpl(), { title: 'Choose a value' });
    await settle();
    const panel = document.querySelector('.dialog-panel') as HTMLElement;
    const select = panel.querySelector('select')!;
    const matches = vi.spyOn(select, 'matches').mockReturnValue(true);
    select.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(document.querySelector('.dialog-panel')).not.toBeNull();
    matches.mockReturnValue(false);
    select.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(document.querySelector('.dialog-panel')).toBeNull();
    matches.mockRestore();
  });

  it('Escape does NOT close when closeOnEscape is false (uncommitted work)', async () => {
    const ref = TestBed.inject(UiDialog).open(host.tpl(), {
      title: 'Discard draft?',
      closeOnEscape: false,
    });
    await settle();
    const panel = document.querySelector('.dialog-panel') as HTMLElement;
    panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(document.querySelector('.dialog-panel')).not.toBeNull();
    ref.close();
  });

  it('traps Tab focus inside the panel', async () => {
    TestBed.inject(UiDialog).open(host.tpl(), { title: 'Discard draft?' });
    await settle();
    const panel = document.querySelector('.dialog-panel') as HTMLElement;
    const focusables = Array.from(panel.querySelectorAll<HTMLElement>('button, [tabindex="0"]'));
    expect(focusables.length).toBeGreaterThanOrEqual(2);
    const last = focusables[focusables.length - 1];
    last.focus();
    expect(document.activeElement).toBe(last);
    panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    // cdkTrapFocus must keep focus within the panel
    expect(panel.contains(document.activeElement)).toBe(true);
  });

  it('shared shell: description, named 44px Close button, size class', async () => {
    TestBed.inject(UiDialog).open(host.tpl(), {
      title: 'Add plant',
      description: 'Create a new plant record.',
      size: 'wide',
    });
    await settle();
    expect(document.querySelector('.ui-dialog-host--wide')).not.toBeNull();
    const panel = document.querySelector('.dialog-panel') as HTMLElement;
    const descId = panel.getAttribute('aria-describedby')!;
    expect(document.getElementById(descId)?.textContent).toBe('Create a new plant record.');
    const close = panel.querySelector<HTMLButtonElement>('.dialog-close')!;
    expect(close.getAttribute('aria-label')).toBe('Close');
    close.click();
    expect(document.querySelector('.dialog-panel')).toBeNull();
  });

  it('closeRequest routes Escape and backdrop through the guard without closing', async () => {
    let requests = 0;
    TestBed.inject(UiDialog).open(host.tpl(), {
      title: 'Edit plant',
      closeRequest: () => requests++,
    });
    await settle();
    const panel = document.querySelector('.dialog-panel') as HTMLElement;
    panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(requests).toBe(1);
    const backdrop = document.querySelector('.cdk-overlay-backdrop') as HTMLElement;
    backdrop.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(requests).toBe(2);
    expect(document.querySelector('.dialog-panel')).not.toBeNull();
  });
});
