import { Component, computed, signal } from '@angular/core';
import { UiSkeleton } from '../ui/skeleton';
import { UiStatus } from '../ui/status';
import {
  StoryBar,
  StoryCredits,
  StoryEmpty,
  StoryError,
  StorySession,
} from './shared';
import { StoryState, plants } from '../../fixtures/collection';

@Component({
  selector: 'story-collection',
  imports: [StoryBar, StoryError, StoryEmpty, StorySession, StoryCredits, UiStatus, UiSkeleton],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    <div class="row spread" style="margin-top:var(--sp-4)">
      <h1 style="margin:0">Your collection</h1>
      <button type="button" class="btn btn-primary">Add plant</button>
    </div>

    <div class="row" role="group" aria-label="Filters" style="margin:var(--sp-4) 0 var(--sp-6)">
      @for (f of filters; track f) {
        <button type="button" class="chip" [attr.aria-pressed]="filter() === f" (click)="filter.set(f)">{{ f }}</button>
      }
      <span class="caption num">{{ visible().length }} plants</span>
    </div>

    @switch (state()) {
      @case ('loading') {
        <div class="grid-cards">
          @for (i of [1,2,3,4,5,6]; track i) { <ui-skeleton ar="4 / 3" /> }
        </div>
      }
      @case ('empty') {
        <story-empty title="No plants yet" hint="Start with one plant and a photo — everything else grows from there." />
      }
      @case ('error') { <story-error title="Collection failed to load" message="We could not reach the server. This is not an empty collection — retry when you can." /> }
      @case ('offline') { <story-error title="You are offline" message="The collection needs a connection. Nothing was changed locally." /> }
      @case ('session') { <story-session /> }
      @case ('conflict') { <story-error title="Tag “rare” already exists" message="Another device created this tag first. Use the existing tag?" /> }
      @default {
        <ul class="grid-cards" style="list-style:none; margin:0; padding:0" aria-label="Plants">
          @for (p of visible(); track p.id) {
            <li class="card" style="padding:0; overflow:hidden">
              <img [src]="p.photo" [alt]="'Photo of ' + p.name" style="width:100%; aspect-ratio:4/3; object-fit:cover; display:block" />
              <div style="padding:var(--sp-3) var(--sp-4) var(--sp-4)">
                <div class="row spread">
                  <strong>{{ p.name }} @if (p.cultivar) { <span class="small" style="font-style:italic">{{ p.cultivar }}</span> }</strong>
                  @if (!p.varietyKnown) { <span class="badge">Unknown variety</span> }
                </div>
                <p class="caption" style="margin:var(--sp-1) 0">{{ p.location }}</p>
                <div class="row spread" style="margin-top:var(--sp-2)">
                  <span class="small num">{{ p.nextCheck }}</span>
                  <ui-status [kind]="p.health" [label]="p.healthLabel" />
                </div>
              </div>
            </li>
          }
        </ul>
      }
    }
  `,
})
export class StoryCollection {
  protected readonly state = signal<StoryState>('ready');
  protected readonly filter = signal<'All' | 'Active' | 'Archived'>('All');
  protected readonly filters = ['All', 'Active', 'Archived'] as const;

  protected readonly visible = computed(() =>
    plants.filter((p) =>
      this.filter() === 'All' ? !p.archived : this.filter() === 'Archived' ? p.archived : !p.archived,
    ),
  );
}
