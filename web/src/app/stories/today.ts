import { Component, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { UiStatus } from '../ui/status';
import { UiSkeleton } from '../ui/skeleton';
import { UiToasts } from '../ui/toast';
import {
  StoryBar,
  StoryCredits,
  StoryEmpty,
  StoryError,
  StorySession,
} from './shared';
import { STORY_STATES, StoryState, careEntries, conditions } from '../../fixtures/collection';

@Component({
  selector: 'story-today',
  imports: [StoryBar, StoryError, StoryEmpty, StorySession, StoryCredits, UiStatus, UiSkeleton, RouterLink],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    @switch (state()) {
      @case ('loading') {
        <h1><ui-skeleton w="320px" ar="40px / 40px" radius="8px" /></h1>
        <div class="grid-cards" style="margin-top:var(--sp-6)">
          @for (i of [1,2,3]; track i) { <ui-skeleton ar="4 / 3" /> }
        </div>
      }
      @case ('empty') {
        <h1>Today in your collection</h1>
        <story-empty title="No checks due" hint="Add a plant and a care plan, and your day will fill itself in." action="Add your first plant" />
      }
      @case ('error') { <h1>Today in your collection</h1><story-error title="Could not load today" message="The care service did not answer. Nothing was changed." /> }
      @case ('offline') { <story-error title="You are offline" message="Showing the last saved view is coming soon; nothing was lost." /> }
      @case ('session') { <h1>Today in your collection</h1><story-session /> }
      @case ('conflict') { <h1>Today in your collection</h1><story-error title="Check completed elsewhere" message="This check was already completed on your phone. Refresh to see the current list." /> }
      @default {
        <div class="row spread">
          <h1 style="margin:0">Today in your collection</h1>
          <span class="badge num">{{ careEntries.length }} checks</span>
        </div>
        <p class="small" style="color:var(--text-muted); margin:var(--sp-1) 0 var(--sp-6)">
          1 overdue · 2 today · 1 upcoming
        </p>

        <div class="grid-cards" style="grid-template-columns: 2fr 1fr; align-items:start">
          <section class="stack" aria-label="Checks due">
            @for (g of groups; track g.name) {
              <div class="card card-raised">
                <h2 style="text-transform:capitalize">{{ g.name }}</h2>
                @for (e of g.items; track e.title) {
                  <div class="row spread" style="padding:var(--sp-2) 0; border-top:1px solid var(--border-decorative)">
                    <div>
                      <strong class="small">{{ e.title }}</strong>
                      <span class="caption"> · {{ e.plant }} · {{ e.isCheck ? 'check' : 'action' }}</span>
                      <p class="caption" style="margin:var(--sp-1) 0 0">{{ e.reason }} <em>({{ e.source }})</em></p>
                    </div>
                    <div class="row">
                      <button type="button" class="btn btn-primary" (click)="complete(e.title)">Complete</button>
                      <button type="button" class="btn" (click)="snooze(e.title)">Snooze</button>
                    </div>
                  </div>
                }
              </div>
            }
          </section>

          <aside class="stack" aria-label="Context">
            <div class="card hero-panel">
              <h2 style="margin:0 0 var(--sp-2)">Current conditions</h2>
              <div class="row spread">
                <span class="num" style="font-size:28px; font-weight:700">{{ conditions.tempC }}°C</span>
                <span class="small" style="color:var(--text-muted)">{{ conditions.summary }} · RH {{ conditions.humidity }}%</span>
              </div>
              @if (stale()) {
                <div class="row wrap" style="margin-top:var(--sp-2)">
                  <ui-status kind="warn" label="Stale" />
                  <span class="caption">{{ conditions.place }} · last fetched 06:10 — 14h ago, shown as older</span>
                </div>
              } @else {
                <p class="caption">{{ conditions.place }} · {{ conditions.freshFor }}</p>
              }
              <img [src]="conditions.photo" alt="Most recent photo of your Monstera" style="width:100%; border-radius:var(--radius-card); aspect-ratio:4/3; object-fit:cover; margin-top:var(--sp-3)" />
            </div>
            <div class="card">
              <h2 style="margin:0 0 var(--sp-2)">Recent journey</h2>
              <p class="small">New leaf fully unfurled · Oct 28</p>
              <p class="small">Repotted one size up · Oct 12</p>
            </div>
            <div class="card">
              <div class="row spread">
                <h2 style="margin:0">Unresolved suggestion</h2>
                <ui-status kind="warn" label="Needs review" />
              </div>
              <p class="small">Calathea edges may indicate underwatering.</p>
              <a class="small" routerLink="/stories/insight">Review evidence</a>
            </div>
          </aside>
        </div>
      }
    }
  `,
})
export class StoryToday {
  protected readonly state = signal<StoryState>('ready');
  protected readonly stale = signal(false);
  protected readonly careEntries = careEntries;
  protected readonly conditions = conditions;
  private readonly toasts = inject(UiToasts);

  constructor() {
    // Visual captures drive fixture states from the URL: ?state=empty,
    // ?state=error, and the ready layout's stale-conditions variant.
    const q = new URLSearchParams(window.location.search).get('state');
    if (q === 'stale') this.stale.set(true);
    else if (q && STORY_STATES.includes(q as StoryState)) this.state.set(q as StoryState);
  }

  protected get groups() {
    return (['overdue', 'today', 'upcoming'] as const)
      .map((name) => ({ name, items: careEntries.filter((e) => e.group === name) }))
      .filter((g) => g.items.length);
  }
  protected complete(title: string): void {
    this.toasts.ok(`Completed “${title}”. Undo is available in the activity log.`);
  }
  protected snooze(title: string): void {
    this.toasts.ok(`Snoozed “${title}” to tomorrow.`);
  }
}
