import type {
  CareAgendaQuery,
  ContextFact,
  PlantCarePlanQuery,
  PreviewCarePlanQuery,
  RuleKind,
  Weekday,
} from '@canopy/contracts';

/** One agenda row: the frozen CareOccurrenceFields fragment. */
export type AgendaItem = CareAgendaQuery['careAgenda']['items'][number];

/** The plan as it rides the self-contained PlantCarePlan read. */
export type PlanFields = NonNullable<NonNullable<PlantCarePlanQuery['plant']>['carePlan']>;
export type PlanVersion = NonNullable<PlanFields['profileVersion']>;
export type PlanRule = PlanVersion['rules'][number];
export type PlanOverride = PlanFields['overrides'][number];
export type Recurrence = PlanRule['recurrence'];
export type PreviewOcc = NonNullable<
  PreviewCarePlanQuery['previewCarePlan']['preview']
>['occurrences'][number];
export type Preview = NonNullable<PreviewCarePlanQuery['previewCarePlan']['preview']>;

/** Browser-local calendar day (YYYY-MM-DD). The agenda groups against this;
 * each occurrence also carries its plan timezone, which the UI states. */
export function localToday(): string {
  return toDateInput(new Date());
}

export function toDateInput(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/** Calendar-day offset on a YYYY-MM-DD string, DST-safe (UTC arithmetic
 * on pure dates never shifts a calendar day). */
export function offsetDate(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return date;
  d.setUTCDate(d.getUTCDate() + days);
  return toDateInput(d);
}

export interface AgendaGroup {
  key: 'overdue' | 'today' | 'upcoming';
  label: string;
  items: AgendaItem[];
}

/** The agenda arrives (currentDueDate ASC); split it against local today.
 * Empty groups are dropped — an empty section is noise, not structure. */
export function groupAgenda(items: AgendaItem[], today: string): AgendaGroup[] {
  const groups: AgendaGroup[] = [
    { key: 'overdue', label: 'Overdue', items: [] },
    { key: 'today', label: 'Today', items: [] },
    { key: 'upcoming', label: 'Upcoming', items: [] },
  ];
  for (const it of items) {
    const g =
      it.currentDueDate < today ? groups[0] : it.currentDueDate === today ? groups[1] : groups[2];
    g.items.push(it);
  }
  return groups.filter((g) => g.items.length > 0);
}

/** Checks and actions must be visibly distinct: a check is an observation
 * with no quantity, an action creates a dated activity. */
export function isCheck(o: AgendaItem): boolean {
  return o.kind === 'CHECK';
}

export function kindLabel(o: AgendaItem): string {
  if (o.kind === 'CHECK') return 'check';
  return (o.activityKind ?? 'action').toLowerCase();
}

/** Due/snoozed/original dates stay legible: snooze moves the effective
 * date but the original never disappears. */
export function dueNote(o: AgendaItem): string {
  if (o.snoozedUntil) {
    return `Snoozed to ${o.currentDueDate} · originally ${o.originalDueDate}`;
  }
  if (o.currentDueDate !== o.originalDueDate) {
    return `Due ${o.currentDueDate} · originally ${o.originalDueDate}`;
  }
  return `Due ${o.currentDueDate}`;
}

/** Missing context is stated, never guessed around: the rule abstained
 * from facts it does not have, and the UI says which. */
export function missingFactsOf(o: AgendaItem): ContextFact[] {
  return o.reason?.missingFacts ?? [];
}

export function factLabel(f: ContextFact): string {
  return f.toLowerCase().replaceAll('_', ' ');
}

const WEEKDAYS: Record<Weekday, string> = {
  MONDAY: 'Mon',
  TUESDAY: 'Tue',
  WEDNESDAY: 'Wed',
  THURSDAY: 'Thu',
  FRIDAY: 'Fri',
  SATURDAY: 'Sat',
  SUNDAY: 'Sun',
};

const WEEKDAY_ORDER: Weekday[] = [
  'MONDAY',
  'TUESDAY',
  'WEDNESDAY',
  'THURSDAY',
  'FRIDAY',
  'SATURDAY',
  'SUNDAY',
];

/** All seven, Monday-first, for weekday pickers. */
export function allWeekdays(): Weekday[] {
  return WEEKDAY_ORDER;
}

export function weekdayLabel(w: Weekday): string {
  return WEEKDAYS[w];
}

const MONTHS = [
  'January',
  'February',
  'March',
  'April',
  'May',
  'June',
  'July',
  'August',
  'September',
  'October',
  'November',
  'December',
];

/** Plain-language recurrence over the frozen union — exhaustive, so a new
 * SDL member breaks the build instead of rendering blank. */
export function describeRecurrence(spec: Recurrence): string {
  switch (spec.__typename) {
    case 'EveryNDaysRecurrence': {
      const anchor =
        spec.anchor === 'LAST_QUALIFYING_COMPLETION' ? 'from each completion' : 'from plan start';
      return `Every ${spec.days} day${spec.days === 1 ? '' : 's'} (${anchor})`;
    }
    case 'WeeklyDaysRecurrence':
      return `Weekly on ${spec.weekdays.map((w) => WEEKDAYS[w]).join(', ')}`;
    case 'AnnualWindowRecurrence': {
      const every = spec.everyYears > 1 ? `Every ${spec.everyYears} years` : 'Yearly';
      const month = MONTHS[(spec.month - 1) % 12] ?? `month ${spec.month}`;
      return `${every} around ${month} ${spec.day} (±${spec.windowDays} days)`;
    }
    case 'OneTimeRecurrence':
      return `Once on ${spec.date}`;
    default: {
      const _never: never = spec;
      return _never;
    }
  }
}

/** Next-five-dates UI: preview occurrences grouped per rule, capped. */
export function nextDatesByRule(
  occurrences: PreviewOcc[],
  limit = 5,
): Array<{
  ruleKey: string;
  kind: RuleKind;
  activityKind: string | null;
  dates: string[];
}> {
  const byRule = new Map<
    string,
    { ruleKey: string; kind: RuleKind; activityKind: string | null; dates: string[] }
  >();
  for (const o of occurrences) {
    let row = byRule.get(o.ruleKey);
    if (!row) {
      row = { ruleKey: o.ruleKey, kind: o.kind, activityKind: o.activityKind ?? null, dates: [] };
      byRule.set(o.ruleKey, row);
    }
    if (row.dates.length < limit && !row.dates.includes(o.dueDate)) row.dates.push(o.dueDate);
  }
  return [...byRule.values()];
}

export function profileLabel(v: {
  version: number;
  species?: string | null;
  category?: string | null;
  cultivar?: string | null;
}): string {
  const name = [v.species ?? v.category, v.cultivar ? `‘${v.cultivar}’` : '']
    .filter(Boolean)
    .join(' ');
  return name ? `${name} · v${v.version}` : `Care profile · v${v.version}`;
}

/** Name from the profile itself (versions carry no species fields). */
export function profileName(p: {
  category: string;
  species: string | null;
  cultivar: string | null;
}): string {
  return [p.species ?? p.category, p.cultivar ? `‘${p.cultivar}’` : ''].filter(Boolean).join(' ');
}

/** Source explanation: where the rule's guidance came from, cited. */
export function ruleCitations(rule: PlanRule): string[] {
  return [...new Set(rule.sourceVersions.map((s) => s.citation))];
}

export interface VersionDiff {
  added: PlanRule[];
  removed: PlanRule[];
  changed: Array<{ from: PlanRule; to: PlanRule }>;
}

/** Profile version upgrade review: what moving the pin from one published
 * version to another would change, rule by rule. */
export function diffVersions(pinned: PlanVersion, current: PlanVersion): VersionDiff {
  const diff: VersionDiff = { added: [], removed: [], changed: [] };
  const byKey = new Map(pinned.rules.map((r) => [r.ruleKey, r]));
  for (const r of current.rules) {
    const old = byKey.get(r.ruleKey);
    if (!old) diff.added.push(r);
    else if (
      JSON.stringify(old.recurrence) !== JSON.stringify(r.recurrence) ||
      old.kind !== r.kind
    ) {
      diff.changed.push({ from: old, to: r });
    }
    byKey.delete(r.ruleKey);
  }
  diff.removed = [...byKey.values()];
  return diff;
}

/** WATER needs volume, FERTILIZE mass or volume — mirrors the server's
 * unit table so the form offers only accepted units. */
export function unitsFor(
  activityKind: string | null | undefined,
): Array<{ value: string; label: string }> {
  if (activityKind === 'WATER')
    return [
      { value: 'ML', label: 'ml' },
      { value: 'L', label: 'L' },
    ];
  if (activityKind === 'FERTILIZE') {
    return [
      { value: 'G', label: 'g' },
      { value: 'KG', label: 'kg' },
      { value: 'ML', label: 'ml' },
      { value: 'L', label: 'L' },
    ];
  }
  return [];
}
