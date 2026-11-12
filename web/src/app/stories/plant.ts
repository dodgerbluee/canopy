import { Component, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { UiSkeleton } from '../ui/skeleton';
import { UiStatus } from '../ui/status';
import { UiTab, UiTabs } from '../ui/tabs';
import { UiToasts } from '../ui/toast';
import {
  StoryBar,
  StoryCredits,
  StoryError,
  StorySession,
} from './shared';
import { StoryState, journeyGroups, plants } from '../../fixtures/collection';

@Component({
  selector: 'story-plant',
  imports: [StoryBar, StoryError, StorySession, StoryCredits, UiStatus, UiSkeleton, UiTabs, UiTab, RouterLink],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    @switch (state()) {
      @case ('loading') {
        <ui-skeleton ar="21 / 9" radius="var(--radius-hero)" />
        <div class="stack" style="margin-top:var(--sp-4)">
          <ui-skeleton w="50%" ar="40px / 40px" radius="8px" />
          <ui-skeleton ar="16 / 4" />
        </div>
      }
      @case ('error') { <story-error title="Plant not found" message="This plant may have been archived on another device." /> }
      @case ('offline') { <story-error title="You are offline" message="The journey needs a connection to load." /> }
      @case ('session') { <story-session /> }
      @case ('conflict') { <story-error title="Revision conflict" message="This note changed while you were editing (server revision 4). Reload your copy before saving." /> }
      @case ('empty') { <story-error title="No journey yet" message="Log your first note or photo to start the timeline." /> }
      @default {
        <div class="card hero-panel" style="display:grid; grid-template-columns: 3fr 2fr; gap:var(--sp-6); padding:0; overflow:hidden">
          <img [src]="plant.photo" [alt]="'Photo of ' + plant.name" style="width:100%; height:100%; object-fit:cover; min-height:280px" />
          <div style="padding:var(--sp-6) var(--sp-6) var(--sp-6) 0">
            <h1 style="margin:0">{{ plant.name }} <span class="small" style="font-style:italic">{{ plant.cultivar }}</span></h1>
            <div class="row" style="margin:var(--sp-2) 0"><ui-status [kind]="plant.health" [label]="plant.healthLabel" /></div>
            <dl class="small" style="display:grid; grid-template-columns:auto 1fr; gap:var(--sp-1) var(--sp-4); margin:var(--sp-4) 0">
              <dt class="caption">Source</dt><dd style="margin:0">Verdant Supply Co. · 2026-09-14</dd>
              <dt class="caption">Acquired</dt><dd style="margin:0">$42.00 · receipt on file</dd>
              <dt class="caption">Location</dt><dd style="margin:0">{{ plant.location }}</dd>
              <dt class="caption">Growth</dt><dd style="margin:0">+18 cm since September</dd>
            </dl>
            <p class="caption mono">plnt-8f21 · provenance expands with monospace detail</p>
          </div>
        </div>

        <div class="row spread" style="margin:var(--sp-6) 0 var(--sp-4)">
          <ui-tabs (selectionChange)="tab.set($event)">
            <button uiTab value="journey">Journey</button>
            <button uiTab value="care">Care</button>
            <button uiTab value="purchases">Purchases</button>
            <button uiTab value="insights">Insights</button>
          </ui-tabs>
          <div class="row">
            <button type="button" class="btn" (click)="toasts.ok('Quick log opened (fixture)')">Quick log</button>
            <button type="button" class="btn btn-primary" (click)="toasts.ok('Camera / file picker opened (fixture)')">Add photo</button>
          </div>
        </div>

        @if (tab() === 'journey') {
          <div class="stack" role="tabpanel" id="tabpanel-journey" aria-labelledby="tab-journey">
            @for (g of journeyGroups; track g.label) {
              <section>
                <h2 class="small" style="color:var(--text-muted); text-transform:uppercase; letter-spacing:.06em">{{ g.label }}</h2>
                <ul class="stack" style="list-style:none; margin:0; padding:0">
                  @for (e of g.events; track e.date + e.title) {
                    <li class="card card-raised">
                      <div class="row spread">
                        <strong class="small">{{ e.title }}</strong>
                        <span class="caption num">{{ e.date }}</span>
                      </div>
                      @if (e.detail) { <p class="caption" style="margin:var(--sp-1) 0">{{ e.detail }}</p> }
                      @if (e.photo) {
                        <img [src]="e.photo" [alt]="e.title" style="width:160px; aspect-ratio:4/3; object-fit:cover; border-radius:var(--radius-field); margin-top:var(--sp-2)" />
                      }
                      @if (e.sourceBadge) { <span class="badge" style="margin-top:var(--sp-2)">{{ e.sourceBadge }}</span> }
                    </li>
                  }
                </ul>
              </section>
            }
            <button type="button" class="btn">Load earlier</button>
          </div>
        }
        @if (tab() === 'care') {
          <div class="card" role="tabpanel" id="tabpanel-care" aria-labelledby="tab-care">
            <h2>Your plan: “Bright indirect, soak &amp; drain”</h2>
            <p class="small">Water every 7 days, adjusted by season; next five dates preview in the editor. Editing the plan cancels 2 future checks — the editor shows exactly which.</p>
          </div>
        }
        @if (tab() === 'purchases') {
          <div class="card" role="tabpanel" id="tabpanel-purchases" aria-labelledby="tab-purchases">
            <h2>Attributed spending: $56.50</h2>
            <p class="small">1 line with unknown cost is excluded from totals. <a routerLink="/stories/purchase">Open purchase</a></p>
          </div>
        }
        @if (tab() === 'insights') {
          <div class="card" role="tabpanel" id="tabpanel-insights" aria-labelledby="tab-insights">
            <div class="row spread"><h2 style="margin:0">1 proposed check</h2><ui-status kind="warn" label="Proposed" /></div>
            <p class="small">Leaf-tip browning pattern. <a routerLink="/stories/insight">Open evidence</a></p>
          </div>
        }
      }
    }
  `,
})
export class StoryPlant {
  protected readonly state = signal<StoryState>('ready');
  protected readonly tab = signal('journey');
  protected readonly plant = plants[0];
  protected readonly journeyGroups = journeyGroups;
  protected readonly toasts = inject(UiToasts);
}
