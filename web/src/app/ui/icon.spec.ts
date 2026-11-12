import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { beforeEach, describe, expect, it } from 'vitest';
import { ICON_PATHS, UiIcon, type UiIconName } from './icon';

@Component({
  standalone: true,
  imports: [UiIcon],
  template: `
    <ui-icon name="overview" />
    <ui-icon name="settings" label="Settings" />
    <ui-icon name="leaf" [size]="32" [stroke]="2.4" />
  `,
})
class Host {}

describe('UiIcon', () => {
  let root: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [Host] }).compileComponents();
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    root = fixture.nativeElement as HTMLElement;
  });

  it('draws the glyph for the name it is given', () => {
    const svgs = [...root.querySelectorAll('svg')];
    expect(svgs[0].querySelector('path')!.getAttribute('d')).toBe(ICON_PATHS.overview);
    expect(svgs[1].querySelector('path')!.getAttribute('d')).toBe(ICON_PATHS.settings);
  });

  it('is hidden from assistive technology by default, because it sits beside a text label', () => {
    const svg = root.querySelector('svg')!;
    expect(svg.getAttribute('aria-hidden')).toBe('true');
    expect(svg.getAttribute('focusable')).toBe('false');
    expect(svg.hasAttribute('aria-label')).toBe(false);
    expect(svg.querySelector('title')).toBeNull();
  });

  it('becomes a named image only when a label is supplied', () => {
    const svg = root.querySelectorAll('svg')[1];
    expect(svg.getAttribute('aria-hidden')).toBeNull();
    expect(svg.getAttribute('role')).toBe('img');
    expect(svg.getAttribute('aria-label')).toBe('Settings');
  });

  it('sizes from the caller and scales the glyph from a fixed 24-unit grid', () => {
    const [defaultSize, sized] = [root.querySelectorAll('svg')[0], root.querySelectorAll('svg')[2]];
    expect(defaultSize.getAttribute('viewBox')).toBe('0 0 24 24');
    expect(defaultSize.getAttribute('width')).toBe('20');
    expect(sized.getAttribute('width')).toBe('32');
    expect(sized.getAttribute('height')).toBe('32');
    expect(defaultSize.getAttribute('stroke-width')).toBe('1.65');
    expect(sized.getAttribute('stroke-width')).toBe('2.4');
  });

  it('has a glyph for every destination and control the shell uses', () => {
    const shell: UiIconName[] = [
      'sprout',
      'overview',
      'leaf',
      'book',
      'calendar',
      'inbox',
      'receipt',
      'settings',
      'home',
      'sun',
      'moon',
      'chevrons',
    ];
    for (const name of shell) expect(ICON_PATHS[name], `missing glyph: ${name}`).toMatch(/^[Mm]/);
  });

  it('draws every glyph as a single stroked path, never a filled shape or an image', () => {
    for (const [name, d] of Object.entries(ICON_PATHS) as [UiIconName, string][]) {
      expect(d, `${name} has no path data`).not.toBe('');
      expect(d, `${name} is not a path`).toMatch(/^[Mm]/);
    }
  });

  it('falls back to a drawn glyph instead of an empty box when the name is unknown', () => {
    const fixture = TestBed.createComponent(UiIcon);
    fixture.componentRef.setInput('name', 'not-a-glyph');
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('svg')).toBeTruthy();
    expect(el.querySelector('path')!.getAttribute('d')).toBe(ICON_PATHS.leaf);
  });

  it('is presentational: it takes no focus and no interaction of its own', () => {
    for (const svg of root.querySelectorAll('svg')) {
      expect(svg.hasAttribute('tabindex')).toBe(false);
      expect(svg.querySelector('a')).toBeNull();
    }
  });
});
