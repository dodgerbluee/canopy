import { describe, expect, it } from 'vitest';
import {
  describeRecurrence,
  diffVersions,
  dueNote,
  groupAgenda,
  kindLabel,
  nextDatesByRule,
  offsetDate,
  profileLabel,
  profileName,
  ruleCitations,
  unitsFor,
  type AgendaItem,
  type PlanRule,
  type PlanVersion,
  type PreviewOcc,
  type Recurrence,
} from './care-view';

function occ(over: Partial<AgendaItem> = {}): AgendaItem {
  return {
    id: 'oc-1',
    planId: 'p-1',
    plantId: 'pl-1',
    ruleKey: 'water',
    kind: 'ACTION',
    activityKind: 'WATER',
    occurrenceKey: 'water:2026-11-02',
    originalDueDate: '2026-11-02',
    currentDueDate: '2026-11-02',
    snoozedUntil: null,
    status: 'DUE',
    completedActivityId: null,
    resolvedAt: null,
    planTimezone: 'America/New_York',
    reason: null,
    ...over,
  };
}

describe('agenda grouping and labelling', () => {
  const today = '2026-11-02';

  it('splits overdue / today / upcoming against the local day and drops empty groups', () => {
    const groups = groupAgenda(
      [
        occ({ id: 'a', currentDueDate: '2026-10-30' }),
        occ({ id: 'b', currentDueDate: '2026-11-02' }),
        occ({ id: 'c', currentDueDate: '2026-11-05' }),
      ],
      today,
    );
    expect(groups.map((g) => g.key)).toEqual(['overdue', 'today', 'upcoming']);
    expect(groups.map((g) => g.items.map((i) => i.id))).toEqual([['a'], ['b'], ['c']]);

    const onlyUpcoming = groupAgenda([occ({ currentDueDate: '2026-11-05' })], today);
    expect(onlyUpcoming.map((g) => g.key)).toEqual(['upcoming']);
  });

  it('checks and actions carry distinct kind badges', () => {
    expect(kindLabel(occ({ kind: 'CHECK', activityKind: null }))).toBe('check');
    expect(kindLabel(occ({ activityKind: 'WATER' }))).toBe('water');
    expect(kindLabel(occ({ activityKind: null }))).toBe('action');
  });

  it('snoozed rows keep the original date visible; plain rows state the due date', () => {
    expect(dueNote(occ())).toBe('Due 2026-11-02');
    expect(dueNote(occ({ currentDueDate: '2026-11-04', snoozedUntil: '2026-11-04' }))).toBe(
      'Snoozed to 2026-11-04 · originally 2026-11-02',
    );
    expect(dueNote(occ({ currentDueDate: '2026-11-03' }))).toBe(
      'Due 2026-11-03 · originally 2026-11-02',
    );
  });

  it('offsetDate crosses month and year bounds on pure calendar days', () => {
    expect(offsetDate('2026-11-30', 1)).toBe('2026-12-01');
    expect(offsetDate('2026-12-31', 1)).toBe('2027-01-01');
    expect(offsetDate('2026-03-01', -1)).toBe('2026-02-28');
    expect(offsetDate('2026-11-02', 0)).toBe('2026-11-02');
  });
});

describe('plain-language recurrence over the frozen union', () => {
  const d = (spec: Recurrence) => describeRecurrence(spec);

  it('every-N-days names the interval and the anchor', () => {
    expect(d({ __typename: 'EveryNDaysRecurrence', days: 7, anchor: 'PLAN_START' })).toBe(
      'Every 7 days (from plan start)',
    );
    expect(
      d({ __typename: 'EveryNDaysRecurrence', days: 1, anchor: 'LAST_QUALIFYING_COMPLETION' }),
    ).toBe('Every 1 day (from each completion)');
  });

  it('weekly-days lists weekday names', () => {
    expect(d({ __typename: 'WeeklyDaysRecurrence', weekdays: ['MONDAY', 'THURSDAY'] })).toBe(
      'Weekly on Mon, Thu',
    );
  });

  it('annual-window names the month, day, window and cadence', () => {
    expect(
      d({ __typename: 'AnnualWindowRecurrence', month: 3, day: 15, windowDays: 10, everyYears: 1 }),
    ).toBe('Yearly around March 15 (±10 days)');
    expect(
      d({ __typename: 'AnnualWindowRecurrence', month: 12, day: 1, windowDays: 3, everyYears: 2 }),
    ).toBe('Every 2 years around December 1 (±3 days)');
  });

  it('one-time states the single date', () => {
    expect(d({ __typename: 'OneTimeRecurrence', date: '2026-12-20' })).toBe('Once on 2026-12-20');
  });
});

describe('preview and version-diff helpers', () => {
  const p = (ruleKey: string, dueDate: string): PreviewOcc => ({
    ruleKey,
    kind: 'ACTION',
    activityKind: 'WATER',
    dueDate,
    reason: null,
  });

  it('nextDatesByRule groups preview dates per rule, capped at five', () => {
    const rows = nextDatesByRule([
      p('water', '2026-11-03'),
      p('water', '2026-11-10'),
      p('water', '2026-11-17'),
      p('water', '2026-11-24'),
      p('water', '2026-12-01'),
      p('water', '2026-12-08'),
      p('mist', '2026-11-04'),
    ]);
    expect(rows).toHaveLength(2);
    expect(rows[0].ruleKey).toBe('water');
    expect(rows[0].dates).toEqual([
      '2026-11-03',
      '2026-11-10',
      '2026-11-17',
      '2026-11-24',
      '2026-12-01',
    ]);
    expect(rows[1].dates).toEqual(['2026-11-04']);
  });

  const rule = (ruleKey: string, recurrence: Recurrence): PlanRule =>
    ({
      ruleKey,
      kind: 'ACTION',
      activityKind: 'WATER',
      requiredFacts: [],
      weatherSensitivity: 'NONE',
      guidanceText: 'g',
      recurrence,
      sourceVersions: [],
    }) as PlanRule;

  const version = (version: number, rules: PlanRule[]): PlanVersion =>
    ({ id: `v${version}`, profileId: 'pr-1', version, rules }) as PlanVersion;

  it('diffVersions reports added, removed and changed rules between pinned and current', () => {
    const pinned = version(2, [
      rule('water', { __typename: 'EveryNDaysRecurrence', days: 7, anchor: 'PLAN_START' }),
      rule('mist', { __typename: 'EveryNDaysRecurrence', days: 2, anchor: 'PLAN_START' }),
    ]);
    const current = version(3, [
      rule('water', { __typename: 'EveryNDaysRecurrence', days: 5, anchor: 'PLAN_START' }),
      rule('feed', { __typename: 'EveryNDaysRecurrence', days: 14, anchor: 'PLAN_START' }),
    ]);
    const diff = diffVersions(pinned, current);
    expect(diff.added.map((r) => r.ruleKey)).toEqual(['feed']);
    expect(diff.removed.map((r) => r.ruleKey)).toEqual(['mist']);
    expect(diff.changed).toHaveLength(1);
    expect(diff.changed[0].from.recurrence).toMatchObject({ days: 7 });
    expect(diff.changed[0].to.recurrence).toMatchObject({ days: 5 });
  });

  it('ruleCitations dedupes source citations', () => {
    const r = rule('water', { __typename: 'EveryNDaysRecurrence', days: 7, anchor: 'PLAN_START' });
    r.sourceVersions = [
      { citation: 'Book A' },
      { citation: 'Book A' },
      { citation: 'Site B' },
    ] as PlanRule['sourceVersions'];
    expect(ruleCitations(r)).toEqual(['Book A', 'Site B']);
  });
});

describe('profile naming and completion units', () => {
  it('profileLabel falls back when the version carries no name fields', () => {
    expect(profileLabel({ version: 3 })).toBe('Care profile · v3');
    expect(
      profileLabel({ version: 2, species: 'Monstera deliciosa', cultivar: 'Thai Constellation' }),
    ).toBe('Monstera deliciosa ‘Thai Constellation’ · v2');
  });

  it('profileName prefers species over category', () => {
    expect(profileName({ category: 'HOUSEPLANT', species: 'Ficus carica', cultivar: null })).toBe(
      'Ficus carica',
    );
    expect(profileName({ category: 'HOUSEPLANT', species: null, cultivar: null })).toBe(
      'HOUSEPLANT',
    );
  });

  it('units mirror the server: water takes volume, fertilize mass or volume, nothing else', () => {
    expect(unitsFor('WATER').map((u) => u.value)).toEqual(['ML', 'L']);
    expect(unitsFor('FERTILIZE').map((u) => u.value)).toEqual(['G', 'KG', 'ML', 'L']);
    expect(unitsFor('PRUNE')).toEqual([]);
    expect(unitsFor(null)).toEqual([]);
  });
});
