import {
  ChangeDetectionStrategy,
  Component,
  OnDestroy,
  OnInit,
  computed,
  inject,
  input,
  signal,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import { CareAgendaDocument, PlantCarePlanDocument } from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { PageStore, type Page } from '../../core/graphql/page-store';
import { UiSkeleton } from '../../ui/skeleton';
import {
  describeRecurrence,
  dueNote,
  factLabel,
  groupAgenda,
  isCheck,
  kindLabel,
  localToday,
  missingFactsOf,
  profileLabel,
  ruleCitations,
  type AgendaItem,
  type PlanFields,
  type PlanRule,
} from './care-view';

/**
 * Plant detail Care tab (T25): the plan as the plant sees it — pinned
 * profile version with cited sources, per-rule plain-language recurrence,
 * and the plant's nearest occurrences with due/snoozed/original dates.
 * Read-only by design: acting on occurrences belongs to the agenda,
 * editing belongs to the plan editor.
 */
@Component({
  selector: 'care-tab',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, UiSkeleton],
  template: `
    @if (!planLoaded()) {
      <div class="stack" aria-busy="true" style="max-width:640px">
        <ui-skeleton ar="auto" maxW="100%" />
        <ui-skeleton ar="auto" maxW="60%" />
      </div>
    } @else if (!plan()) {
      <section class="card" style="max-width:640px">
        <h2 style="margin:0 0 var(--sp-2)">No care plan yet</h2>
        <p class="caption">
          A plan pins reviewed care guidance (with sources) to this plant and turns it into dated
          checks and actions in the agenda. Nothing is scheduled until a plan exists.
        </p>
        <div>
          <a class="btn btn-primary" [routerLink]="['/care/plan', plantId()]">Create care plan</a>
        </div>
      </section>
    } @else {
      <section
        class="card card-raised"
        style="max-width:640px; margin-bottom:var(--sp-4)"
        aria-labelledby="cp-h"
      >
        <div class="row spread wrap">
          <h2 id="cp-h" style="margin:0">
            {{
              plan()!.profileVersion
                ? 'Your plan: ' + profileLabel(plan()!.profileVersion!)
                : 'Owner-defined plan'
            }}
          </h2>
          <a class="btn" [routerLink]="['/care/plan', plantId()]">Edit plan</a>
        </div>
        <p class="caption" style="margin:var(--sp-2) 0 0">
          {{ plan()!.enabled ? 'Enabled' : 'Paused' }} · dates computed in {{ plan()!.timezone }} ·
          notifications at {{ plan()!.notificationTime }} · rev {{ plan()!.revision }}
        </p>
      </section>

      @if (rules().length) {
        <section
          class="card card-raised"
          style="max-width:640px; margin-bottom:var(--sp-4)"
          aria-labelledby="cr-h"
        >
          <h2 id="cr-h" style="margin:0 0 var(--sp-2)">Rules and where they come from</h2>
          @for (r of rules(); track r.ruleKey) {
            <div style="border-top:1px solid var(--border-decorative); padding:var(--sp-3) 0">
              <strong class="small"
                >{{ r.ruleKey }}
                <span class="caption">
                  ·
                  {{
                    r.kind === 'CHECK' ? 'check' : (r.activityKind ?? 'action').toLowerCase()
                  }}</span
                >
              </strong>
              <p class="caption" style="margin:var(--sp-1) 0">
                {{ describe(r.recurrence) }} — {{ r.guidanceText }}
              </p>
              @if (citations(r).length) {
                <p class="caption" style="margin:0">Source: {{ citations(r).join('; ') }}</p>
              }
            </div>
          }
        </section>
      }

      <section class="card card-raised" style="max-width:640px" aria-labelledby="ca-h">
        <div class="row spread">
          <h2 id="ca-h" style="margin:0">Coming due for this plant</h2>
          <a class="btn" routerLink="/care">Open agenda</a>
        </div>
        @if (store.loading() && store.items().length === 0) {
          <div class="stack" aria-busy="true" style="margin-top:var(--sp-3)">
            @for (i of [1, 2]; track i) {
              <ui-skeleton ar="auto" maxW="100%" />
            }
          </div>
        } @else if (store.error()) {
          <p class="caption" role="alert" style="margin:var(--sp-2) 0 0">{{ store.error() }}</p>
        } @else if (store.items().length === 0) {
          <p class="caption" style="margin:var(--sp-2) 0 0">Nothing due in the next 60 days.</p>
        } @else {
          <ul class="stack" style="list-style:none; margin:var(--sp-3) 0 0; padding:0">
            @for (g of groups(); track g.key) {
              <li>
                <h3 class="caption" style="margin:0 0 var(--sp-1)">{{ g.label }}</h3>
                <ul class="stack" style="list-style:none; margin:0; padding:0">
                  @for (o of g.items; track o.id) {
                    <li class="card" [class.is-check]="isCheck(o)">
                      <div class="row spread wrap">
                        <strong class="small">{{ o.ruleKey }}</strong>
                        <span class="row wrap">
                          <span class="badge" [class.badge-check]="isCheck(o)">{{
                            kindLabel(o)
                          }}</span>
                          <span class="badge num">{{ dueNote(o) }}</span>
                        </span>
                      </div>
                      @if (missingFactsOf(o).length) {
                        <p class="caption" style="margin:var(--sp-1) 0 0">
                          <strong>Needs context:</strong>
                          {{ missingFactsOf(o).map(factLabel).join(', ') }}
                        </p>
                      }
                    </li>
                  }
                </ul>
              </li>
            }
          </ul>
        }
      </section>
    }
  `,
  styles: [
    `
      .row.wrap {
        flex-wrap: wrap;
      }
      .is-check {
        border-left: 3px solid var(--accent);
      }
      .badge-check {
        font-style: italic;
      }
    `,
  ],
})
export class CareTab implements OnInit, OnDestroy {
  readonly plantId = input.required<string>();

  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  protected readonly store: PageStore<AgendaItem>;
  /** The plan rides its own self-contained read (PlantCarePlan): the
   * collection's PlantDetail operation stays free of care fragments. */
  protected readonly plan = signal<PlanFields | null>(null);
  protected readonly planLoaded = signal(false);

  constructor() {
    this.store = new PageStore((after) => this.fetchPage(after));
  }

  ngOnInit(): void {
    void this.loadPlan();
    void this.store.refresh();
  }

  ngOnDestroy(): void {
    this.abort.abort();
    this.store.destroy();
  }

  private async loadPlan(): Promise<void> {
    const res = await this.gql.query(
      PlantCarePlanDocument,
      { id: this.plantId() },
      this.abort.signal,
    );
    if (res.kind === 'ok' && res.data) this.plan.set(res.data.plant?.carePlan ?? null);
    this.planLoaded.set(true);
  }

  protected readonly groups = computed(() => groupAgenda(this.store.items(), localToday()));
  protected readonly profileLabel = profileLabel;
  protected readonly describe = describeRecurrence;
  protected readonly citations = ruleCitations;
  protected readonly isCheck = isCheck;
  protected readonly kindLabel = kindLabel;
  protected readonly dueNote = dueNote;
  protected readonly missingFactsOf = missingFactsOf;
  protected readonly factLabel = factLabel;
  protected rules(): PlanRule[] {
    return this.plan()?.profileVersion?.rules ?? [];
  }

  private fetchPage(after: string | null): Promise<GqlResult<Page<AgendaItem>>> {
    return this.gql
      .query(CareAgendaDocument, { plantId: this.plantId(), first: 10, after }, this.abort.signal)
      .then((res) => {
        if (res.kind !== 'ok') return res;
        if (!res.data) return { kind: 'ok', data: null, errors: res.errors };
        const conn = res.data.careAgenda;
        return {
          kind: 'ok',
          errors: res.errors,
          data: {
            items: conn.items,
            endCursor: conn.pageInfo.endCursor,
            hasNextPage: conn.pageInfo.hasNextPage,
          },
        };
      });
  }
}
