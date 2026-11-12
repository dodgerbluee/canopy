import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { photoCredits } from '../../fixtures/collection';

@Component({
  selector: 'story-index',
  imports: [RouterLink],
  template: `
    <h1>Visual references</h1>
    <p class="small" style="color:var(--text-muted)">
      Static compositions over synthetic fixtures — no production API. Every screen
      renders the full state contract from its state selector chips.
    </p>
    <ul class="stack" style="list-style:none; padding:0; max-width:560px">
      @for (r of refs; track r.path) {
        <li>
          <a class="card row spread" [routerLink]="r.path" style="text-decoration:none">
            <strong>{{ r.title }}</strong>
            <span class="caption">{{ r.note }}</span>
          </a>
        </li>
      }
    </ul>
    <p class="caption" style="margin-top:var(--sp-6)">{{ credits }}</p>
  `,
})
export class StoryIndex {
  protected readonly credits = photoCredits;
  protected readonly refs = [
    { path: '/stories/today', title: 'Today', note: 'checks due, conditions, journey' },
    { path: '/stories/plants', title: 'Collection', note: 'grid, filters, empty/loading' },
    { path: '/stories/plant', title: 'Plant detail', note: 'hero, tabs, journey' },
    { path: '/stories/capture', title: 'Capture', note: 'upload queue, failures' },
    { path: '/stories/compare', title: 'Compare', note: 'side-by-side + overlay' },
    { path: '/stories/purchase', title: 'Purchase editor', note: 'OCR drafts, allocations' },
    { path: '/stories/purchases', title: 'Purchases ledger', note: 'spending card, exact totals, voided row' },
    { path: '/stories/insight', title: 'Insight evidence', note: 'evidence sheet' },
  ];
}

@Component({
  selector: 'story-credits-page',
  template: `
    <h1>Settings (fixture)</h1>
    <p class="small">Theme override lives in the top bar. System preference is respected until you override it.</p>
    <div class="card">
      <h2>Fixture photo attribution</h2>
      <p class="small">{{ credits }}</p>
      <p class="caption mono">/assets/photos/LICENSES.json</p>
    </div>
  `,
})
export class StoryCreditsPage {
  protected readonly credits = photoCredits;
}
