import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it } from 'vitest';
import { UiPhotoViewer } from './photo-viewer';
const photos = [1, 2].map((i) => ({
  id: String(i),
  src: `/api/media/${i}/display`,
  original: `/api/media/${i}/original`,
  alt: `Plant photo ${i}`,
  detail: `Capture ${i}`,
}));
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('Fullscreen photo viewer', () => {
  afterEach(() => {
    TestBed.inject(UiPhotoViewer).close();
    TestBed.resetTestingModule();
  });
  it('shows the uncropped display image and saves the corresponding original after navigation', async () => {
    const viewer = TestBed.inject(UiPhotoViewer);
    viewer.open(photos);
    await settle();
    const dialog = document.querySelector<HTMLElement>('[role=dialog]')!;
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(dialog.querySelector('.viewer-stage img')?.getAttribute('src')).toBe(photos[0].src);
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    await settle();
    expect(dialog.querySelector('.viewer-stage img')?.getAttribute('src')).toBe(photos[1].src);
    expect(dialog.querySelector('a[download]')?.getAttribute('href')).toBe(photos[1].original);
    expect(dialog.querySelector('[aria-label="Next photo"]')?.hasAttribute('disabled')).toBe(true);
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    await settle();
    expect(dialog.querySelector('.viewer-stage img')?.getAttribute('src')).toBe(photos[1].src);
  });
  it('closes on Escape and restores focus to the opener', async () => {
    const opener = document.createElement('button');
    document.body.append(opener);
    opener.focus();
    TestBed.inject(UiPhotoViewer).open(photos);
    await settle();
    document
      .querySelector('[role=dialog]')!
      .dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(document.querySelector('ui-photo-viewer')).toBeNull();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });
  it('reports image failure without hiding the original download, and allows moving to another photo', async () => {
    TestBed.inject(UiPhotoViewer).open(photos);
    await settle();
    document.querySelector('.viewer-stage img')!.dispatchEvent(new Event('error'));
    await settle();
    expect(document.querySelector('[role=alert]')?.textContent).toContain('could not be loaded');
    expect(document.querySelector('a[download]')?.getAttribute('href')).toBe(photos[0].original);
    (document.querySelector('[aria-label="Next photo"]') as HTMLButtonElement).click();
    await settle();
    expect(document.querySelector('[role=alert]')).toBeNull();
    expect(document.querySelector('.viewer-stage img')?.getAttribute('src')).toBe(photos[1].src);
  });
});
