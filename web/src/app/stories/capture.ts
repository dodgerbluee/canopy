import { Component, signal } from '@angular/core';
import { UiStatus } from '../ui/status';
import {
  StoryBar,
  StoryCredits,
  StoryError,
  StorySession,
} from './shared';
import { StoryState, captureQueue } from '../../fixtures/collection';

@Component({
  selector: 'story-capture',
  imports: [StoryBar, StoryError, StorySession, StoryCredits, UiStatus],
  template: `
    <story-bar [current]="state()" (set)="state.set($event)" />
    <story-credits />

    @switch (state()) {
      @case ('error') { <story-error title="Upload failed" message="IMG_2043 failed its checksum. A fresh reservation was created; your other photos are safe." /> }
      @case ('offline') { <story-error title="Connection lost" message="Completed uploads are already on the server. Failed items restart from byte zero after you reselect the file." /> }
      @case ('session') { <story-session /> }
      @case ('empty') { <story-error title="Nothing selected" message="Choose photos from your camera roll or take a new photo." /> }
      @case ('conflict') { <story-error title="Wrong file detected" message="IMG_2042 did not match its reservation size/checksum. Reselect the file to continue." /> }
      @default {
        <h1>Add photos</h1>
        <p class="small" style="color:var(--text-muted)">
          From your camera or library — select several at once. Photos stay private to your workspace.
        </p>
        <label class="btn btn-primary" for="capture-input" style="cursor:pointer">
          Choose photos
        </label>
        <input id="capture-input" type="file" accept="image/*" multiple class="visually-only" (change)="picked = $any($event.target).files?.length ?? 0" />
        @if (picked()) { <span class="caption">{{ picked() }} selected (processing is a fixture)</span> }

        <ul class="stack" style="list-style:none; margin:var(--sp-6) 0 0; padding:0" aria-label="Upload queue">
          @for (f of queue; track f.name) {
            <li class="card card-raised">
              <div class="row spread">
                <strong class="small mono">{{ f.name }}</strong>
                @switch (f.state) {
                  @case ('done') { <ui-status kind="ok" label="Uploaded" /> }
                  @case ('processing') { <ui-status kind="warn" label="Processing" /> }
                  @case ('failed') { <ui-status kind="danger" label="Failed" /> }
                  @default { <span class="badge">Queued</span> }
                }
              </div>
              @if (f.state === 'processing') {
                <div role="progressbar" [attr.aria-valuenow]="f.pct" aria-valuemin="0" aria-valuemax="100"
                     aria-label="Upload progress for {{ f.name }}"
                     style="height:8px; border-radius:99px; background:var(--surface-canvas); border:1px solid var(--border); margin:var(--sp-2) 0">
                  <div style="height:100%; width:{{ f.pct }}%; background:var(--accent); border-radius:99px"></div>
                </div>
              }
              @if (f.error) { <p class="field-error" style="margin-top:var(--sp-2)">{{ f.error }}</p> }
              <div class="row" style="margin-top:var(--sp-2)">
                <span class="caption">{{ f.plant }} · {{ f.viewpoint }}</span>
                @if (f.state === 'failed') {
                  <button type="button" class="btn">Retry</button>
                  <button type="button" class="btn btn-danger">Remove</button>
                }
              </div>
            </li>
          }
        </ul>
        <p class="caption" style="margin-top:var(--sp-4)">
          Pending uploads survive a reload for 24 h; incomplete ones restart from byte zero after you reselect the file.
        </p>
      }
    }
  `,
})
export class StoryCapture {
  protected readonly state = signal<StoryState>('ready');
  protected readonly queue = captureQueue;
  protected picked = signal(0);
}
