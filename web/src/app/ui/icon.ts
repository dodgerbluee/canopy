import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';

/**
 * Explicit, closed set of 24×24 stroke glyphs taken from the approved mock
 * (docs/plan/tasks/UI01.md: "consistent SVG icons"). Paths are rendered as a
 * bound `d` attribute — no innerHTML, no icon font, no network fetch.
 */
export type UiIconName =
  | 'pencil'
  | 'filter'
  | 'flower'
  | 'fruit'
  | 'prune'
  | 'shield'
  | 'sprout'
  | 'leaf'
  | 'overview'
  | 'book'
  | 'calendar'
  | 'receipt'
  | 'home'
  | 'search'
  | 'moon'
  | 'sun'
  | 'plus'
  | 'camera'
  | 'arrow'
  | 'arrow-up-right'
  | 'back'
  | 'rain'
  | 'drop'
  | 'sparkles'
  | 'check'
  | 'location'
  | 'image'
  | 'close'
  | 'upload'
  | 'clock'
  | 'settings'
  | 'inbox'
  | 'chevrons'
  | 'ellipsis';

export const ICON_PATHS: Record<UiIconName, string> = {
  pencil: 'm16 3 5 5M4 20l4-1L21 6a2 2 0 0 0-4-4L4 15l-1 6Z',
  filter: 'M4 6h16M7 12h10M10 18h4M8 4v4M16 10v4M12 16v4',
  flower:
    'M12 12c-9 0-9-8-4-8 3 0 4 5 4 8Zm0 0c0-9 8-9 8-4 0 3-5 4-8 4Zm0 0c9 0 9 8 4 8-3 0-4-5-4-8Zm0 0c0 9-8 9-8 4 0-3 5-4 8-4Z',
  fruit: 'M12 7c-6-5-11 1-8 8 3 7 6 6 8 5 2 1 5 2 8-5 3-7-2-13-8-8Zm0 0c-1-5 2-6 5-5 0 3-2 5-5 5Z',
  prune: 'm8 9 12 12M8 15 20 3M9 7a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm0 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z',
  shield: 'M12 2 3 6v6c0 6 9 10 9 10s9-4 9-10V6Z',
  sprout: 'M12 21v-9M12 15C4 16 3 6 3 6s10-1 9 9Zm0-4C12 3 21 3 21 3s1 9-9 9',
  leaf: 'M20 4C8 2 3 8 5 15s12 8 15-11ZM5 20l9-10',
  overview: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z',
  book: 'M12 6v15M3 4c4-1 7 0 9 2 2-2 5-3 9-2v15c-4-1-7 0-9 2-2-2-5-3-9-2Z',
  calendar:
    'M8 2v4M16 2v4M3 10h18M5 4h14a2 2 0 0 1 2 2v14H3V6a2 2 0 0 1 2-2ZM7 14h2M13 14h2M7 18h2',
  receipt: 'M5 2l3 2 4-2 4 2 3-2v20l-3-2-4 2-4-2-3 2ZM9 8h6M9 12h6M9 16h3',
  home: 'm3 10 9-7 9 7M5 9v12h5v-7h4v7h5V9',
  search: 'M16 16l5 5M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',
  moon: 'M21 13A9 9 0 0 1 11 3a9 9 0 1 0 10 10',
  sun: 'M12 3V1M12 23v-2M3 12H1M23 12h-2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4M17 12a5 5 0 1 1-10 0 5 5 0 0 1 10 0',
  plus: 'M12 5v14M5 12h14',
  camera: 'M3 6h4l2-3h6l2 3h4v14H3ZM16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0',
  arrow: 'M4 12h16m-6-6 6 6-6 6',
  'arrow-up-right': 'M6 18 18 6M6 6h12v12',
  back: 'M20 12H4m6-6-6 6 6 6',
  rain: 'M7 14H6a4 4 0 0 1 0-8 6 6 0 0 1 11-1 4.5 4.5 0 0 1 1 9h-1M8 17l-1 4m6-4-1 4m6-4-1 4',
  drop: 'M12 2S4 10 4 15a8 8 0 0 0 16 0c0-5-8-13-8-13Z',
  sparkles: 'm12 3 2.4 6.6L21 12l-6.6 2.4L12 21l-2.4-6.6L3 12l6.6-2.4ZM20 2v4m-2-2h4',
  check: 'm5 12 4 4L19 6',
  location: 'M19 9c0 5-7 12-7 12S5 14 5 9a7 7 0 1 1 14 0ZM14 9a2 2 0 1 1-4 0 2 2 0 0 1 4 0',
  image: 'M3 3h18v18H3Zm0 14 6-6 4 4 3-3 5 5M8 7h.01',
  close: 'm6 6 12 12M6 18 18 6',
  upload: 'M12 16V3m-5 5 5-5 5 5M3 15v6h18v-6',
  clock: 'M12 6v6l4 2M22 12a10 10 0 1 1-20 0 10 10 0 0 1 20 0',
  settings:
    'm9 3 1-2h4l1 2 3 2 2 1v4l-2 1-1 3v3l-3 2-2-1-3 0-2 1-3-2v-3l-1-3-2-1V6l2-1 3-2ZM15 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0',
  // Not in the mock set: same 24×24 grid, same 1.65 stroke, tray + inbox bar.
  inbox: 'M3 13h5l1.5 3h5L16 13h5M3 13l3-9h12l3 9v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z',
  chevrons: 'm8 9 4-4 4 4m-8 6 4 4 4-4',
  // Same 24×24 grid and round caps as the `image` glyph's dot: three dots.
  ellipsis: 'M5 12h.01M12 12h.01M19 12h.01',
};

/**
 * Pure presentation primitive: decorative by default (aria-hidden), pass
 * `label` when the glyph itself carries meaning. Never fetches, never mutates.
 */
@Component({
  selector: 'ui-icon',
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <svg
      viewBox="0 0 24 24"
      [attr.width]="size()"
      [attr.height]="size()"
      [attr.stroke-width]="stroke()"
      [attr.aria-hidden]="label() ? null : 'true'"
      [attr.role]="label() ? 'img' : null"
      [attr.aria-label]="label() || null"
      focusable="false"
    >
      <path [attr.d]="path()" />
    </svg>
  `,
  styles: `
    :host {
      display: inline-flex;
      flex: none;
      vertical-align: middle;
    }
    svg {
      display: block;
      fill: none;
      stroke: currentColor;
      stroke-linecap: round;
      stroke-linejoin: round;
    }
  `,
})
export class UiIcon {
  readonly name = input.required<UiIconName>();
  /** rendered box in px; the glyph itself scales from the 24-unit viewBox */
  readonly size = input(20);
  /** accessible name — omit for purely decorative glyphs */
  readonly label = input('');
  /** mock stroke weight */
  readonly stroke = input(1.65);

  protected readonly path = computed(() => ICON_PATHS[this.name()] ?? ICON_PATHS.leaf);
}
