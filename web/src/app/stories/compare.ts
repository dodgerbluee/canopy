import { Component, signal } from '@angular/core';
import {
  StoryBar,
  StoryCredits,
  StoryError,
  StorySession,
} from './shared';
import { StoryState, journeyGroups } from '../../fixtures/collection';

@Component({
  selector: 'story-compare',
  imports: [StoryBar, StoryError, StorySession, StoryCredits],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    @switch (state()) {
      @case ('error') { <story-error title="Could not load photos" message="One of the selected photos failed to load. Try another pair." /> }
      @case ('offline') { <story-error title="You are offline" message="Comparison needs both photos; they are stored on the server." /> }
      @case ('session') { <story-session /> }
      @case ('empty') { <story-error title="Pick two photos" message="Choose a left and a right photo to compare." /> }
      @case ('conflict') { <story-error title="Selection changed" message="The right-hand photo was replaced on another device. Reload the selection." /> }
      @default {
        <h1>Compare photos</h1>
        <p class="small" style="color:var(--text-muted)">
          Viewpoints differ — this pair was shot from different angles. No growth percentage is invented.
        </p>

        <div class="row" style="margin:var(--sp-4) 0">
          <button type="button" class="chip" [attr.aria-pressed]="mode() === 'side'" (click)="mode.set('side')">Side by side</button>
          <button type="button" class="chip" [attr.aria-pressed]="mode() === 'overlay'" (click)="mode.set('overlay')">Overlay slider</button>
          <button type="button" class="chip" [attr.aria-pressed]="crop()" (click)="crop.set(!crop())">Fill crop</button>
        </div>

        @if (mode() === 'side') {
          <div class="grid-cards" style="grid-template-columns:1fr 1fr">
            @for (p of pair; track p.date) {
              <figure class="card" style="margin:0; padding:0; overflow:hidden">
                <img [src]="p.photo" [alt]="'Photo from ' + p.date"
                     [style.object-fit]="crop() ? 'cover' : 'contain'"
                     [style.background]="'var(--surface-canvas)'"
                     style="width:100%; aspect-ratio:4/3; display:block" />
                <figcaption class="small" style="padding:var(--sp-3) var(--sp-4)">
                  <strong class="num">{{ p.date }}</strong> — {{ p.caption }}
                </figcaption>
              </figure>
            }
          </div>
        } @else {
          <div class="card" style="padding:0; overflow:hidden; position:relative">
            <div style="position:relative; aspect-ratio:4/3">
              <img [src]="pair[0].photo" [alt]="'Left photo from ' + pair[0].date"
                   style="position:absolute; inset:0; width:100%; height:100%; object-fit:contain; background:var(--surface-canvas)" />
              <img [src]="pair[1].photo" [alt]="'Right photo from ' + pair[1].date"
                   [style.clip-path]="'inset(0 0 0 ' + split() + '%)'"
                   style="position:absolute; inset:0; width:100%; height:100%; object-fit:contain" />
              <div [style.left.%]="split()" style="position:absolute; top:0; bottom:0; width:2px; background:var(--accent); transform:translateX(-1px)"></div>
            </div>
            <div style="padding:var(--sp-3) var(--sp-4)">
              <label class="field-label" for="split">Overlay position</label>
              <input id="split" type="range" min="0" max="100" step="1" class="input"
                     [value]="split()" (input)="split.set(+$any($event.target).value)"
                     aria-label="Reveal right photo percentage" />
            </div>
          </div>
          <p class="caption">Arrow keys move the handle; Home/End jump to either photo.</p>
        }

        <div class="row" style="margin-top:var(--sp-4)">
          <button type="button" class="btn">Swap left/right</button>
          <button type="button" class="btn">Change selection</button>
        </div>
      }
    }
  `,
})
export class StoryCompare {
  protected readonly state = signal<StoryState>('ready');
  protected readonly mode = signal<'side' | 'overlay'>('side');
  protected readonly crop = signal(false);
  protected readonly split = signal(50);
  protected readonly pair = [
    { ...journeyGroups[1].events[0], caption: 'Two cataphylls drying out' },
    { ...journeyGroups[0].events[0], caption: 'New leaf fully unfurled' },
  ];
}
