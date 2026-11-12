import { Component } from '@angular/core';

/** Release-aware root: R1 features are assembled by later tasks; this page
 * explains status instead of a blank placeholder (04-experience.md). */
@Component({
  selector: 'app-release-page',
  template: `
    <main style="max-width:560px; margin:10dvh auto; padding:0 var(--sp-4)">
      <h1>Canopy</h1>
      <p class="small" style="color:var(--text-muted)">
        Your private plant observatory. The first release is being assembled —
        collection, journal and purchases will open here.
      </p>
      <p class="caption">build: foundation lane</p>
    </main>
  `,
})
export class ReleasePage {}
