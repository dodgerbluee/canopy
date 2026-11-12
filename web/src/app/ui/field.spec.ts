import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { UiField } from './field';
import { focusFirstInvalid } from './a11y';

@Component({
  imports: [UiField],
  template: `
    <ui-field label="Plant name" inputId="f-name" />
    <ui-field label="Notes" inputId="f-notes" control="textarea" hint="Anything you want to remember" error="Too long (max 500)" />
  `,
})
class Host {}

describe('UiField', () => {
  it('associates label with control via for/id', () => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    const label = document.querySelector('label[for="f-name"]')!;
    const input = document.querySelector('#f-name')!;
    expect(label.textContent).toContain('Plant name');
    expect(input.tagName).toBe('INPUT');
  });

  it('wires hint and error into aria-describedby and sets aria-invalid', () => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    const notes = document.querySelector('#f-notes')!;
    expect(notes.getAttribute('aria-invalid')).toBe('true');
    const described = notes.getAttribute('aria-describedby') ?? '';
    expect(described).toContain('f-notes-hint');
    expect(described).toContain('f-notes-error');
    expect(document.querySelector('#f-notes-error')!.textContent).toContain('Too long');
  });

  it('focusFirstInvalid focuses the first invalid control', () => {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    const focused = focusFirstInvalid(root);
    expect(focused?.id).toBe('f-notes');
    expect(document.activeElement?.id).toBe('f-notes');
  });
});
