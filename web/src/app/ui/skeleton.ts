import { Component, input } from '@angular/core';

/** Layout-accurate skeleton: reserves the real content box (aspect ratio),
 * never a generic grey bar that shifts layout on load. */
@Component({
  selector: 'ui-skeleton',
  template: '<span class="visually-only">Loading…</span>',
  host: {
    class: 'skeleton',
    '[style.aspect-ratio]': 'ar()',
    '[style.width]': 'w()',
    '[style.max-width]': 'maxW()',
    '[style.border-radius]': 'radius()',
  },
})
export class UiSkeleton {
  readonly ar = input('16 / 9');
  readonly w = input('100%');
  readonly maxW = input('');
  readonly radius = input('var(--radius-card)');
}
