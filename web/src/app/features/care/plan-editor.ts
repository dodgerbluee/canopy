import { requestId } from '../../core/request-id';
import { ChangeDetectionStrategy, Component, inject, signal, viewChild } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import {
  CareProfileDetailDocument,
  CareProfilePickerDocument,
  CreateCarePlanDocument,
  PlantCarePlanDocument,
  PlantDetailDocument,
  PreviewCarePlanDocument,
  UpdateCarePlanDocument,
  type CareProfilePickerQuery,
  type CareProfileFieldsFragment,
  type PlanRuleOverrideInput,
  type RecurrenceSpecInput,
  type Weekday,
} from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiToasts } from '../../ui/toast';
import { conflictNote, generalProblems, type PayloadProblem } from '../collection/shared';
import {
  allWeekdays,
  describeRecurrence,
  diffVersions,
  nextDatesByRule,
  profileLabel,
  profileName,
  ruleCitations,
  weekdayLabel,
  type PlanFields,
  type PlanRule,
  type PlanVersion,
  type Preview,
} from './care-view';

/** One rule's editor draft: enable toggle plus an optional recurrence
 * override (every-N-days or weekly weekdays — the cadence knobs owners
 * actually turn; rarer shapes stay display-only). */
interface RuleDraft {
  ruleKey: string;
  enabled: boolean;
  days: string; // everyNDays override, '' = none
  weekdays: Weekday[] | null; // weeklyDays override, null = none
}

/**
 * Care plan editor (T25): create or edit a plant's plan with a live
 * recurrence preview — the next five dates per rule, computed by the
 * server against the plant's real context, never guessed client-side.
 * Timezone changes must preview first and be confirmed; saving shows
 * exactly which open occurrences it would cancel. A plan pinned to an
 * older profile version gets an upgrade review with a rule-by-rule diff.
 *
 * The editor is modal data entry (UI02): every state — loading, not-found
 * and the form — projects into the shared UiEntryDialog shell, whose header
 * owns the title (h2), so no page h1 or inline card/max-width remains.
 * Every native control, id, validation and mutation payload is unchanged.
 * Cancel goes through requestClose(), so an unsaved draft asks Keep
 * editing / Discard; a completed save marks the form pristine and approves
 * the entry before the success navigation, so it never prompts.
 */
@Component({
  selector: 'care-plan-editor',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [UiEntryDialog],
  template: `
    <!-- Modal data entry: the shared shell wraps EVERY state so #entry stays
         registered and the portal never disappears mid-navigation (the
         conflict reload). The shell owns the title (h2). -->
    <ui-entry-dialog
      #entry
      [open]="true"
      [title]="plan() ? 'Edit care plan' : 'New care plan'"
      [description]="
        plant()
          ? 'Plan for ' + plant()!.name + '. Dates come from the server, never guessed.'
          : 'Care plan schedule.'
      "
      size="wide"
      [busy]="busy()"
      (dismissed)="dismiss()"
    >
      @if (state() === 'loading') {
        <p class="caption">Loading plan…</p>
      } @else if (state() === 'missing') {
        <section class="form-section" role="alert">
          <h2>Plant not found</h2>
          <p class="caption">
            It may have been archived-and-purged, or it belongs to another workspace.
          </p>
          <div class="form-actions">
            <button type="button" class="btn btn-primary" (click)="dismiss()">
              Back to collection
            </button>
          </div>
        </section>
      } @else if (plant(); as p) {
        @if (banner()) {
          <section class="form-section" role="alert">
            <p class="caption" style="margin:0">{{ banner() }}</p>
          </section>
        }

        @if (plan(); as pl) {
          <p class="caption" style="margin:0">
            Revision {{ pl.revision }} · {{ pl.enabled ? 'enabled' : 'paused' }} ·
            {{
              pl.profileVersion
                ? profile()
                  ? profileName(profile()!) + ' · v' + pl.profileVersion.version
                  : profileLabel(pl.profileVersion)
                : 'owner-defined rules only'
            }}
          </p>
        }

        <section class="form-section" aria-labelledby="pe-sched-h">
          <h2 id="pe-sched-h" style="margin:0">Schedule</h2>
          <div class="row wrap" style="gap:var(--sp-3); align-items:flex-end">
            <div>
              <label class="field-label" for="pe-tz">Timezone (IANA)</label>
              <input
                id="pe-tz"
                class="input"
                [value]="tz()"
                (input)="tz.set($any($event.target).value); previewed.set(false)"
              />
            </div>
            <div>
              <label class="field-label" for="pe-nt">Notification time</label>
              <input
                id="pe-nt"
                class="input"
                type="time"
                [value]="notifTime()"
                (input)="notifTime.set($any($event.target).value)"
              />
            </div>
            <label class="chip chip-check">
              <input
                type="checkbox"
                [checked]="enabled()"
                (change)="enabled.set($any($event.target).checked)"
              />
              Plan enabled
            </label>
          </div>
          @if (tzChanged()) {
            <p class="caption" style="margin:var(--sp-3) 0 0">
              Changing the timezone re-dates every future occurrence. Preview first, then confirm
              below — past history never moves.
            </p>
            <label class="chip chip-check" style="margin-top:var(--sp-2)">
              <input
                type="checkbox"
                [checked]="confirmRecalc()"
                (change)="confirmRecalc.set($any($event.target).checked)"
              />
              I understand: future dates will be recalculated in {{ tz() }}
            </label>
          }
        </section>

        @if (!plan() && profiles().length > 0) {
          <section class="form-section" aria-labelledby="pe-prof-h">
            <h2 id="pe-prof-h" style="margin:0">Care profile</h2>
            <label class="field-label" for="pe-profile">Profile version to pin</label>
            <select id="pe-profile" class="input" (change)="pickProfile($any($event.target).value)">
              <option value="">No profile — owner-defined rules only</option>
              @for (pr of profiles(); track pr.id) {
                <option
                  [value]="pr.currentVersion!.id"
                  [selected]="pinnedVersionId() === pr.currentVersion!.id"
                >
                  {{ profileName(pr) }} · v{{ pr.currentVersion!.version }}
                </option>
              }
            </select>
          </section>
        }

        @if (upgrade(); as up) {
          <section class="form-section" aria-labelledby="up-h">
            <div class="row spread">
              <h2 id="up-h" style="margin:0">Profile upgrade available</h2>
              <span class="badge num"
                >v{{ pinnedVersion().version }} → v{{ up.current.version }}</span
              >
            </div>
            <ul class="caption" style="margin:var(--sp-2) 0">
              @for (r of up.diff.added; track r.ruleKey) {
                <li>Adds {{ r.ruleKey }}: {{ describeRecurrence(r.recurrence) }}</li>
              }
              @for (r of up.diff.removed; track r.ruleKey) {
                <li>Removes {{ r.ruleKey }}</li>
              }
              @for (c of up.diff.changed; track c.to.ruleKey) {
                <li>
                  {{ c.to.ruleKey }}: {{ describeRecurrence(c.from.recurrence) }} →
                  {{ describeRecurrence(c.to.recurrence) }}
                </li>
              }
            </ul>
            <button type="button" class="btn btn-primary" (click)="applyUpgrade(up.current)">
              Apply v{{ up.current.version }}
            </button>
          </section>
        }

        @if (rules().length > 0) {
          <section class="form-section" aria-labelledby="pe-rules-h">
            <h2 id="pe-rules-h" style="margin:0">Rules</h2>
            @for (r of rules(); track r.ruleKey) {
              <div style="border-top:1px solid var(--border-decorative); padding:var(--sp-3) 0">
                <div class="row spread wrap">
                  <strong class="small"
                    >{{ r.ruleKey }}
                    <span class="caption">
                      ·
                      {{
                        r.kind === 'CHECK' ? 'check' : (r.activityKind ?? 'action').toLowerCase()
                      }}</span
                    >
                  </strong>
                  <label class="chip chip-check">
                    <input
                      type="checkbox"
                      [checked]="draft(r.ruleKey).enabled"
                      (change)="toggleRule(r.ruleKey, $any($event.target).checked)"
                    />
                    Enabled
                  </label>
                </div>
                <p class="caption" style="margin:var(--sp-1) 0">
                  {{ describeRecurrence(r.recurrence) }} — {{ r.guidanceText }}
                </p>
                @if (ruleCitations(r).length) {
                  <p class="caption" style="margin:0">Source: {{ ruleCitations(r).join('; ') }}</p>
                }
                @if (r.requiredFacts.length) {
                  <p class="caption" style="margin:0">
                    Needs:
                    {{ r.requiredFacts.join(', ').toLowerCase().replaceAll('_', ' ') }}
                  </p>
                }
                @if (
                  draft(r.ruleKey).enabled && r.recurrence.__typename === 'EveryNDaysRecurrence'
                ) {
                  <div
                    class="row wrap"
                    style="align-items:flex-end; gap:var(--sp-2); margin-top:var(--sp-2)"
                  >
                    <label class="field-label" for="pe-d-{{ r.ruleKey }}"
                      >Override every-N-days</label
                    >
                    <input
                      id="pe-d-{{ r.ruleKey }}"
                      class="input"
                      inputmode="numeric"
                      style="width:90px"
                      [value]="draft(r.ruleKey).days"
                      placeholder="{{ r.recurrence.days }}"
                      (input)="setDays(r.ruleKey, $any($event.target).value)"
                    />
                    <span class="caption"
                      >days (blank = keep the profile’s {{ r.recurrence.days }})</span
                    >
                  </div>
                }
                @if (
                  draft(r.ruleKey).enabled && r.recurrence.__typename === 'WeeklyDaysRecurrence'
                ) {
                  <div
                    class="row wrap"
                    role="group"
                    [attr.aria-label]="'Weekdays for ' + r.ruleKey"
                    style="margin-top:var(--sp-2)"
                  >
                    @for (w of weekdays(); track w) {
                      <label class="chip chip-check">
                        <input
                          type="checkbox"
                          [checked]="
                            (draft(r.ruleKey).weekdays ?? r.recurrence.weekdays).includes(w)
                          "
                          (change)="toggleWeekday(r.ruleKey, w, $any($event.target).checked)"
                        />
                        {{ weekdayLabel(w) }}
                      </label>
                    }
                  </div>
                }
              </div>
            }
          </section>
        }

        <div class="form-actions">
          <button type="button" class="btn" (click)="entry.requestClose()">Cancel</button>
          <button type="button" class="btn" [disabled]="previewBusy()" (click)="runPreview()">
            {{ previewBusy() ? 'Previewing…' : 'Preview next dates' }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            [disabled]="busy() || !canSave()"
            (click)="save()"
          >
            {{ plan() ? 'Save plan' : 'Create plan' }}
          </button>
        </div>

        @if (preview(); as pv) {
          <section class="form-section" aria-labelledby="pv-h">
            <h2 id="pv-h" style="margin:0 0 var(--sp-2)">Next dates in {{ tz() }}</h2>
            @if (pv.missingFacts.length) {
              <p class="caption" style="margin:0 0 var(--sp-2)">
                <strong>No schedule is guessed while context is missing:</strong>
                {{ pv.missingFacts.join(', ').toLowerCase().replaceAll('_', ' ') }}
              </p>
            }
            @for (row of nextDates(pv); track row.ruleKey) {
              <div style="border-top:1px solid var(--border-decorative); padding:var(--sp-2) 0">
                <strong class="small">{{ row.ruleKey }}</strong>
                <span class="badge">{{
                  row.kind === 'CHECK' ? 'check' : (row.activityKind ?? 'action').toLowerCase()
                }}</span>
                <span class="caption num"> {{ row.dates.join(' · ') }}</span>
              </div>
            }
            @if (pv.occurrences.length === 0) {
              <p class="caption" style="margin:var(--sp-2) 0">
                No dates within the horizon — every rule abstains or is disabled.
              </p>
            }
            @if (pv.wouldCancel.length) {
              <p class="caption" style="margin:var(--sp-3) 0 0">
                <strong>Saving cancels {{ pv.wouldCancel.length }} open occurrence(s):</strong>
                {{ pv.wouldCancel.map((o) => o.ruleKey + ' on ' + o.currentDueDate).join('; ') }}.
                History already recorded is never removed.
              </p>
            }
          </section>
        }
      }
    </ui-entry-dialog>
  `,
  styles: [
    `
      .row.wrap {
        flex-wrap: wrap;
      }
      .chip-check {
        display: inline-flex;
        gap: var(--sp-2);
        align-items: center;
        cursor: pointer;
      }
      .chip-check input {
        accent-color: var(--accent);
      }
      .field-label {
        display: block;
      }
      /* the shared .entry-form grid owns section spacing; headings keep a
         small gap from their first control */
      .form-section h2 {
        margin-block: 0 var(--sp-2);
      }
    `,
  ],
})
export class CarePlanEditor {
  protected readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly gql = inject(GraphQLClient);
  private readonly toasts = inject(UiToasts);
  private readonly session = inject(SessionService);

  /** The modal host: Cancel routes through requestClose() (dirty-guarded),
   * a completed save calls markPristine() so the success navigation is
   * never prompted, and a programmatic profile upgrade calls markDirty(). */
  private readonly entry = viewChild.required<UiEntryDialog>('entry');

  protected readonly state = signal<'loading' | 'ready' | 'missing'>('loading');
  protected readonly plant = signal<NonNullable<
    import('@canopy/contracts').PlantDetailQuery['plant']
  > | null>(null);
  protected readonly plan = signal<PlanFields | null>(null);
  protected readonly profiles = signal<CareProfilePickerQuery['careProfiles']['items']>([]);
  protected readonly banner = signal('');
  protected readonly busy = signal(false);
  protected readonly previewBusy = signal(false);
  protected readonly preview = signal<Preview | null>(null);
  protected readonly previewed = signal(false);

  protected readonly tz = signal('UTC');
  protected readonly notifTime = signal('09:00');
  protected readonly enabled = signal(true);
  protected readonly confirmRecalc = signal(false);
  protected readonly pinnedVersionId = signal<string | null>(null);
  protected readonly currentProfileVersion = signal<PlanVersion | null>(null);
  /** Full content of the version picked in the create-mode picker: the
   * picker query stays light, so the version body is fetched on pick
   * (upgrade review already loads it through CareProfileDetail). */
  protected readonly pickedVersion = signal<PlanVersion | null>(null);
  protected readonly profile = signal<CareProfileFieldsFragment | null>(null);
  protected readonly ruleDrafts = signal(new Map<string, RuleDraft>());

  protected readonly weekdays = allWeekdays;
  protected readonly weekdayLabel = weekdayLabel;
  protected readonly profileLabel = profileLabel;
  protected readonly profileName = profileName;
  protected readonly describeRecurrence = describeRecurrence;
  protected readonly ruleCitations = ruleCitations;
  protected readonly nextDates = (pv: Preview) => nextDatesByRule(pv.occurrences);

  constructor() {
    const plantId = this.route.snapshot.paramMap.get('plantId');
    if (plantId) void this.load(plantId);
    else this.state.set('missing');
  }

  protected pinnedVersion(): PlanVersion {
    const v = this.pinnedVersionOrNull();
    if (!v) throw new Error('no pinned version');
    return v;
  }

  /** Safe parent for Cancel/dismissal: the care agenda behind this modal. */
  protected dismiss(): void {
    void this.router.navigate(['/care']);
  }

  protected rules(): PlanRule[] {
    return this.pinnedVersionOrNull()?.rules ?? [];
  }

  private pinnedVersionOrNull(): PlanVersion | null {
    return this.plan()?.profileVersion ?? this.pickedVersion() ?? this.currentProfileVersion();
  }

  protected draft(ruleKey: string): RuleDraft {
    return (
      this.ruleDrafts().get(ruleKey) ?? {
        ruleKey,
        enabled: true,
        days: '',
        weekdays: null,
      }
    );
  }

  protected tzChanged(): boolean {
    const pl = this.plan();
    return !!pl && pl.timezone !== this.tz();
  }

  protected canSave(): boolean {
    return !this.tzChanged() || (this.confirmRecalc() && this.previewed());
  }

  protected upgrade(): {
    current: PlanVersion;
    diff: ReturnType<typeof diffVersions>;
  } | null {
    const pl = this.plan();
    const cur = this.currentProfileVersion();
    if (!pl?.profileVersion || !cur || cur.id === pl.profileVersion.id) return null;
    return { current: cur, diff: diffVersions(pl.profileVersion, cur) };
  }

  protected toggleRule(ruleKey: string, on: boolean): void {
    this.mutateDraft(ruleKey, (d) => ({ ...d, enabled: on }));
    this.previewed.set(false);
  }

  protected setDays(ruleKey: string, v: string): void {
    const clean = v.trim();
    if (clean !== '' && (!/^\d+$/.test(clean) || Number(clean) < 1 || Number(clean) > 365)) {
      this.banner.set('Override days must be a whole number from 1 to 365, or blank.');
      return;
    }
    this.banner.set('');
    this.mutateDraft(ruleKey, (d) => ({ ...d, days: clean }));
    this.previewed.set(false);
  }

  protected toggleWeekday(ruleKey: string, w: Weekday, on: boolean): void {
    const rule = this.rules().find((r) => r.ruleKey === ruleKey);
    if (!rule || rule.recurrence.__typename !== 'WeeklyDaysRecurrence') return;
    const cur = this.draft(ruleKey).weekdays ?? rule.recurrence.weekdays;
    const next = on ? [...cur, w] : cur.filter((x) => x !== w);
    this.mutateDraft(ruleKey, (d) => ({ ...d, weekdays: next }));
    this.previewed.set(false);
  }

  protected pickProfile(versionId: string): void {
    this.pinnedVersionId.set(versionId || null);
    this.pickedVersion.set(null);
    this.ruleDrafts.set(new Map());
    this.preview.set(null);
    this.previewed.set(false);
    // CareProfileDetail is keyed by profile id; the picker row carries it.
    const pr = this.profiles().find((p) => p.currentVersion?.id === versionId);
    if (versionId && pr) void this.loadPickedVersion(pr.id, versionId);
  }

  private async loadPickedVersion(profileId: string, versionId: string): Promise<void> {
    const res = await this.gql.query(CareProfileDetailDocument, {
      id: profileId,
    });
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind === 'ok' && res.data?.careProfile) {
      const v = res.data.careProfile.currentVersion ?? null;
      if (this.pinnedVersionId() === versionId && v && v.id === versionId) {
        this.pickedVersion.set(v);
      }
    }
  }

  protected applyUpgrade(v: PlanVersion): void {
    this.pinnedVersionId.set(v.id);
    this.currentProfileVersion.set(v);
    this.ruleDrafts.set(new Map());
    this.previewed.set(false);
    // programmatic change: the native input tracking never fires, so flag it
    this.entry().markDirty();
    this.toasts.ok(`Plan will pin v${v.version} — preview, then save.`);
  }

  protected async runPreview(): Promise<void> {
    const p = this.plant();
    if (!p || this.previewBusy()) return;
    this.previewBusy.set(true);
    try {
      const res = await this.gql.query(PreviewCarePlanDocument, {
        input: {
          plantId: p.id,
          ...(this.pinnedVersionId() ? { profileVersionId: this.pinnedVersionId() } : {}),
          timezone: this.tz(),
          notificationTime: this.notifTime(),
          overrides: this.overridePayload(),
        },
      });
      if (res.kind === 'auth') {
        this.session.markExpired();
        return;
      }
      if (res.kind !== 'ok' || !res.data) {
        this.banner.set('Preview failed — the server did not answer. Nothing was changed.');
        return;
      }
      const problems = res.data.previewCarePlan.problems as PayloadProblem[];
      if (problems.length > 0) {
        this.banner.set(problems[0].message);
        this.preview.set(null);
        return;
      }
      this.banner.set('');
      this.preview.set(res.data.previewCarePlan.preview);
      this.previewed.set(true);
    } finally {
      this.previewBusy.set(false);
    }
  }

  protected async save(): Promise<void> {
    const p = this.plant();
    if (!p || this.busy() || !this.canSave()) return;
    this.busy.set(true);
    try {
      if (this.plan()) {
        const pl = this.plan()!;
        const input: Record<string, unknown> = {
          id: pl.id,
          expectedRevision: pl.revision,
          timezone: this.tz(),
          notificationTime: this.notifTime(),
          enabled: this.enabled(),
          overrides: this.overridePayload(),
        };
        if (this.pinnedVersionId() && this.pinnedVersionId() !== pl.profileVersion?.id) {
          input['profileVersionId'] = this.pinnedVersionId();
        }
        if (this.tzChanged()) input['confirmRecalculation'] = true;
        const res = await this.gql.mutate(UpdateCarePlanDocument, {
          input,
          clientMutationId: `${requestId()}:update-care-plan`,
        } as never);
        // successful save: the form is pristine and the entry is approved
        // before the success navigation, so it never prompts
        this.entry().markPristine();
        this.afterSave(
          res,
          'updateCarePlan',
          p.id,
          'Plan saved — future dates regenerated, history kept.',
        );
      } else {
        const res = await this.gql.mutate(CreateCarePlanDocument, {
          input: {
            plantId: p.id,
            ...(this.pinnedVersionId() ? { profileVersionId: this.pinnedVersionId() } : {}),
            timezone: this.tz(),
            notificationTime: this.notifTime(),
            enabled: this.enabled(),
            overrides: this.overridePayload(),
          },
          clientMutationId: `${requestId()}:create-care-plan`,
        } as never);
        // successful save: pristine + approved before the success navigation
        this.entry().markPristine();
        this.afterSave(res, 'createCarePlan', p.id, 'Plan created — the agenda fills from today.');
      }
    } finally {
      this.busy.set(false);
    }
  }

  private afterSave(
    res: GqlResult<Record<string, { problems?: PayloadProblem[] } | null>>,
    field: string,
    plantId: string,
    okLabel: string,
  ): void {
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind !== 'ok') {
      this.banner.set('Could not reach the server. Nothing was changed.');
      return;
    }
    const problems = (res.data?.[field]?.problems ?? []) as PayloadProblem[];
    if (problems.length > 0) {
      const note =
        conflictNote(problems, 'This plan') ||
        generalProblems(problems).join(' ') ||
        problems[0].message;
      this.banner.set(`${note} Your entries are kept — check the revision above and save again.`);
      void this.load(plantId, true); // refresh the plan (and its revision) under the kept draft
      return;
    }
    // actual success: the entry is already pristine+approved (save() marked it
    // before calling here), so navigating closes the modal without prompting
    // instead of reloading the editor under it.
    this.toasts.ok(okLabel);
    this.banner.set('');
    void this.router.navigate(['/care']);
  }

  private overridePayload(): PlanRuleOverrideInput[] {
    const out: PlanRuleOverrideInput[] = [];
    for (const r of this.rules()) {
      const d = this.draft(r.ruleKey);
      const spec = this.overrideSpec(r, d);
      if (!d.enabled) out.push({ ruleKey: r.ruleKey, enabled: false });
      else if (spec)
        out.push({
          ruleKey: r.ruleKey,
          enabled: true,
          recurrenceOverride: spec,
        });
    }
    return out;
  }

  private overrideSpec(r: PlanRule, d: RuleDraft): RecurrenceSpecInput | null {
    if (r.recurrence.__typename === 'EveryNDaysRecurrence' && d.days !== '') {
      return {
        everyNDays: { days: Number(d.days), anchor: r.recurrence.anchor },
      };
    }
    if (
      r.recurrence.__typename === 'WeeklyDaysRecurrence' &&
      d.weekdays &&
      JSON.stringify([...d.weekdays].sort()) !== JSON.stringify([...r.recurrence.weekdays].sort())
    ) {
      return { weeklyDays: { weekdays: d.weekdays } };
    }
    return null;
  }

  private mutateDraft(ruleKey: string, f: (d: RuleDraft) => RuleDraft): void {
    this.ruleDrafts.update((m) => {
      const next = new Map(m);
      next.set(ruleKey, f(this.draft(ruleKey)));
      return next;
    });
  }

  private async load(plantId: string, keepDraft = false): Promise<void> {
    this.state.set('loading');
    const [res, planRes] = await Promise.all([
      this.gql.query(PlantDetailDocument, { id: plantId }),
      this.gql.query(PlantCarePlanDocument, { id: plantId }),
    ]);
    if (res.kind === 'auth') {
      this.session.markExpired();
      return;
    }
    if (res.kind !== 'ok' || !res.data || !res.data.plant) {
      this.state.set('missing');
      return;
    }
    this.plant.set(res.data.plant);
    const pl =
      planRes.kind === 'ok' && planRes.data ? (planRes.data.plant?.carePlan ?? null) : null;
    this.plan.set(pl);
    if (!keepDraft) {
      this.tz.set(pl?.timezone ?? 'UTC');
      this.notifTime.set(pl?.notificationTime ?? '09:00');
      this.enabled.set(pl?.enabled ?? true);
    }
    if (pl?.profileVersion) {
      this.pinnedVersionId.set(pl.profileVersion.id);
      this.applyDraftFromPlan(pl);
      void this.loadProfileCurrent(pl.profileVersion.profileId);
    } else {
      void this.loadProfiles();
    }
    this.state.set('ready');
  }

  /** Existing overrides on the loaded plan become visible draft state. */
  private applyDraftFromPlan(pl: PlanFields): void {
    const m = new Map<string, RuleDraft>();
    for (const ov of pl.overrides) {
      const d: RuleDraft = {
        ruleKey: ov.ruleKey,
        enabled: ov.enabled,
        days: '',
        weekdays: null,
      };
      if (ov.recurrenceOverride?.__typename === 'EveryNDaysRecurrence')
        d.days = String(ov.recurrenceOverride.days);
      if (ov.recurrenceOverride?.__typename === 'WeeklyDaysRecurrence')
        d.weekdays = ov.recurrenceOverride.weekdays;
      m.set(ov.ruleKey, d);
    }
    this.ruleDrafts.set(m);
  }

  private async loadProfileCurrent(profileId: string): Promise<void> {
    const res = await this.gql.query(CareProfileDetailDocument, {
      id: profileId,
    });
    if (res.kind === 'ok' && res.data?.careProfile) {
      this.profile.set(res.data.careProfile as CareProfileFieldsFragment);
      const cur = res.data.careProfile.currentVersion ?? null;
      this.currentProfileVersion.set(cur);
      // The editor's profile label reads the aggregate name; CareProfileDetail
      // is keyed by version id, so cache it for the label too.
      if (cur && !this.pickedVersion()) this.pickedVersion.set(cur);
    }
  }

  private async loadProfiles(): Promise<void> {
    // first stays at the SDL default: the profile subtree (versions → rules
    // → recurrence → citations) is charged per row against the complexity
    // cap, and a picker only needs the near page.
    const res = await this.gql.query(CareProfilePickerDocument, { first: 25 });
    if (res.kind === 'ok' && res.data) {
      this.profiles.set(res.data.careProfiles.items.filter((p) => p.currentVersion !== null));
    }
  }
}
