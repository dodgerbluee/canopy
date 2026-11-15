import { describe, expect, it } from 'vitest';
import { draftIntent, draftPeriod, isCivilDate, planDraft } from './planning-form-data';
import { PlanningAttempt, periodLabel, validViewMonth } from './planning-data';

describe('planning intent and civil periods', () => {
  it('keeps unknown quantities, money and dates null while accepting a name-only idea', () => {
    const draft = planDraft(null, 'USD', true);
    draft.name = '  Another fig  ';
    const parsed = draftIntent(draft);
    expect(parsed.errors).toEqual({});
    expect(parsed.intent.name).toBe('Another fig');
    for (const key of [
      'desiredQuantity',
      'budget',
      'targetPeriod',
      'method',
      'intendedLocationId',
      'intendedPlacement',
      'scientificName',
      'cultivar',
      'notes',
    ] as const)
      expect(parsed.intent[key]).toBeNull();
  });
  it('distinguishes explicit zero from unknown and preserves each currency exponent', () => {
    for (const [currency, amount, minor] of [
      ['USD', '0', '0'],
      ['USD', '10000000000000', '1000000000000000'],
      ['JPY', '1237', '1237'],
      ['KWD', '1.250', '1250'],
    ]) {
      const draft = planDraft(null, currency);
      draft.name = 'Plan';
      draft.budget = amount;
      expect(draftIntent(draft).intent.budget).toEqual({ currency, minorUnits: minor });
    }
    const draft = planDraft(null, 'USD');
    draft.name = 'Plan';
    draft.budget = '10000000000000.01';
    expect(draftIntent(draft).errors['budget']).toContain('exceeds');
  });
  it('keeps seasons and years coarse rather than fabricating a day', () => {
    const draft = planDraft(null, 'USD');
    draft.precision = 'YEAR';
    draft.year = '2027';
    expect(draftPeriod(draft).period).toEqual({ precision: 'YEAR', year: 2027 });
    draft.precision = 'SEASON';
    draft.season = 'WINTER';
    expect(draftPeriod(draft).period).toEqual({
      precision: 'SEASON',
      season: { season: 'WINTER', year: 2027 },
    });
    expect(periodLabel(draftPeriod(draft).period)).toBe('Winter 2027');
  });
  it('rejects impossible or reversed civil dates and invalid optional values', () => {
    expect(isCivilDate('2027-02-29')).toBe(false);
    expect(isCivilDate('2028-02-29')).toBe(true);
    const draft = planDraft(null, 'USD');
    draft.name = 'Plan';
    draft.precision = 'DATE_RANGE';
    draft.fromDay = '2027-04-02';
    draft.toDay = '2027-04-01';
    draft.quantity = '0';
    expect(draftIntent(draft).errors['period']).toBeTruthy();
    expect(draftIntent(draft).errors['quantity']).toBeTruthy();
    expect(validViewMonth('9999-02')).toBe(false);
    expect(validViewMonth('2027-1')).toBe(false);
  });
  it('reuses an ambiguous mutation retry but resets the intention for changed or new editors', () => {
    const attempt = new PlanningAttempt();
    const a = attempt.for({ name: 'Fig' });
    expect(attempt.for({ name: 'Fig' })).toBe(a);
    expect(attempt.for({ name: 'Lime' })).not.toBe(a);
    expect(new PlanningAttempt().for({ name: 'Fig' })).not.toBe(a);
  });
});
