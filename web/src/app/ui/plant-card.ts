import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { UiIcon } from './icon';
import { UiPhoto } from './photo';

/**
 * Photo-led plant card (UI01.b): the mock's composition — cover image, name,
 * one quiet metadata line, and a footer of recorded facts. Presentation only:
 * every input is a primitive or a pre-built URL string, the primary action is
 * a real router link, and there is no fetching, no contract import and no
 * mutation logic here. The feature page owns archive/restore and renders it
 * inside the overflow control, never inside the card body.
 */
@Component({
  selector: 'ui-plant-card',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, UiIcon, UiPhoto],
  template: `
    <a class="card" [class.list]="layout() === 'list'" [routerLink]="link()">
      <ui-photo
        class="photo"
        [src]="photo()"
        [alt]="'Photo of ' + name()"
        category="plant"
        framing="auto"
        [ratio]="layout() === 'list' ? '1 / 1' : '4 / 3'"
      />
      @if (badge()) {
        <span class="state">{{ badge() }}</span>
      }
      <div class="info">
        <div class="title-row">
          <h3>
            {{ name() }}
            @if (cultivar()) {
              <span class="cultivar">{{ cultivar() }}</span>
            }
          </h3>
          <ui-icon name="arrow-up-right" [size]="16" />
        </div>
        <p>{{ meta() }}</p>
        @if (quiet()) {
          <p class="quiet">{{ quiet() }}</p>
        }
        <div class="foot">
          <span>
            <ui-icon name="image" [size]="14" />
            <span class="num"
              >{{ photoCount() }} {{ photoCount() === 1 ? 'photo' : 'photos' }}</span
            >
          </span>
          <span>{{ since() }}</span>
        </div>
      </div>
    </a>
    @if (showActions()) {
      <div class="overflow">
        <details>
          <summary class="icon-button" [attr.aria-label]="'More actions for ' + name()">
            <ui-icon name="ellipsis" [size]="18" [stroke]="2.6" />
          </summary>
          <div class="overflow-panel">
            <ng-content />
          </div>
        </details>
      </div>
    }
  `,
  styles: `
    :host {
      position: relative;
      display: block;
    }
    .card {
      display: block;
      padding: 0;
      gap: 0;
      background: var(--surface-base);
      border: 1px solid var(--border-decorative);
      border-radius: var(--radius-card);
      overflow: hidden;
      text-decoration: none;
      color: inherit;
      transition:
        border-color var(--dur-control) var(--ease-out),
        box-shadow var(--dur-control) var(--ease-out);
    }
    .card:hover {
      border-color: var(--border);
      box-shadow: var(--shadow);
    }
    .photo {
      display: block;
      width: 100%;
    }
    /* list mode: compact horizontal card, fixed square photo column, body takes the rest */
    .card.list {
      display: grid;
      grid-template-columns: 112px minmax(0, 1fr);
      align-items: stretch;
    }
    .card.list .photo {
      height: 100%;
    }
    .card.list .info {
      min-width: 0;
    }
    .card.list .foot {
      flex-wrap: wrap;
    }
    @media (max-width: 767px) {
      .card.list {
        grid-template-columns: 88px minmax(0, 1fr);
      }
      .card.list .info {
        padding: var(--sp-3);
      }
    }
    .state {
      position: absolute;
      top: var(--sp-3);
      left: var(--sp-3);
      z-index: 2;
      padding: 2px var(--sp-2);
      border-radius: var(--radius-pill);
      background: var(--surface-base);
      border: 1px solid var(--border-decorative);
      color: var(--text-primary);
      font-size: var(--fs-caption);
      font-weight: 700;
    }
    .info {
      padding: var(--sp-4);
      display: grid;
      gap: var(--sp-2);
    }
    .title-row {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: var(--sp-2);
    }
    h3 {
      margin: 0;
      font-size: 17px;
      line-height: 24px;
      font-weight: 650;
      letter-spacing: -0.01em;
      color: var(--text-primary);
    }
    .cultivar {
      font-size: var(--fs-small);
      font-style: italic;
      color: var(--text-muted);
    }
    .title-row ui-icon {
      color: var(--text-muted);
      flex: none;
    }
    p {
      margin: 0;
      font-size: var(--fs-small);
      line-height: var(--lh-small);
      color: var(--text-muted);
    }
    .quiet {
      font-size: var(--fs-caption);
      line-height: var(--lh-caption);
    }
    .foot {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: var(--sp-2);
      border-top: 1px solid var(--border-decorative);
      padding-top: var(--sp-3);
      font-size: var(--fs-caption);
      line-height: var(--lh-caption);
      color: var(--text-muted);
    }
    .foot span {
      display: inline-flex;
      align-items: center;
      gap: var(--sp-1);
    }
    .overflow {
      position: absolute;
      top: var(--sp-2);
      right: var(--sp-2);
      z-index: 3;
    }
    details {
      position: relative;
    }
    summary {
      width: 44px;
      height: 44px;
      border-radius: var(--radius-pill);
      display: inline-flex;
      align-items: center;
      justify-content: center;
      cursor: pointer;
      background: var(--surface-base);
      border: 1px solid var(--border-decorative);
    }
    summary::-webkit-details-marker {
      display: none;
      list-style: none;
    }
    details[open] summary {
      background: var(--surface-raised);
      color: var(--text-primary);
    }
    .overflow-panel {
      position: absolute;
      right: 0;
      top: calc(100% + 4px);
      z-index: 4;
      background: var(--surface-base);
      border: 1px solid var(--border);
      border-radius: var(--radius-field);
      box-shadow: var(--shadow);
      padding: var(--sp-2);
      display: grid;
      gap: var(--sp-1);
      min-width: 160px;
    }
  `,
})
export class UiPlantCard {
  /** destination for the card's primary action */
  readonly link = input.required<string[]>();
  readonly name = input.required<string>();
  /** cover derivative URL, or null when the plant has no uploaded photo */
  readonly photo = input<string | null>(null);
  /** one quiet line of recorded facts (location, placement, group count) */
  readonly meta = input('');
  /** honest metadata, not a warning: "Cultivar unknown", "Archived", "Dead" */
  readonly badge = input('');
  /** quiet metadata line under the facts (e.g. "Cultivar unknown") */
  readonly quiet = input('');
  readonly cultivar = input<string | null>(null);
  /** actual photoCount from the record */
  readonly photoCount = input(0);
  /** recorded date, already formatted by the caller ("Added Nov 1, 2026") */
  readonly since = input('');
  /**
   * Renders the overflow control (and therefore the projected archive/restore
   * action). Off by default: the card's only action is its main link, so a
   * control with nothing in it would be a dead tab stop.
   */
  readonly showActions = input(false);
  /** grid keeps the 4/3 cover; list is the compact horizontal variant */
  readonly layout = input<'grid' | 'list'>('grid');
}
