import { describe, it, expect } from 'vitest';
import { actualPlantInput, gardenDraft } from './garden-draft';
import type { Plan } from './planning-data';
const plan = {
  name: 'Future fig',
  scientificName: 'Ficus carica',
  cultivar: 'Alma',
  category: 'FIG',
  desiredQuantity: 6,
  intendedPlacement: 'GROUND',
  budget: { minorUnits: '15000', currency: 'USD' },
  targetPeriod: { precision: 'YEAR', year: 2027 },
} as Plan;
describe('reviewed actual plant values', () => {
  it('carries identity while keeping desired count, future site, dates and budget out of actual records', () => {
    const d = gardenDraft(plan, 'EUR');
    const actual = actualPlantInput(d, '2026-11-15');
    expect(actual).toMatchObject({
      name: 'Future fig',
      scientificName: 'Ficus carica',
      cultivar: 'Alma',
      count: null,
      plantedOn: null,
      initialContext: null,
      initialPurchase: null,
      acquisition: null,
    });
    expect(d.currency).toBe('EUR');
    expect(d.price).toBe('');
    expect(d.count).toBe('');
    expect(d.placement).toBe('');
  });
  it('records explicitly free pricing with exact currency precision independently of the estimate', () => {
    const d = {
      ...gardenDraft(plan, 'JPY'),
      price: '0',
      tax: '0',
      shipping: '0',
      count: '2',
      acquiredOn: '2026-10-25',
    };
    const actual = actualPlantInput(d, '2026-11-15');
    expect(actual.count).toBe(2);
    expect(actual.initialPurchase).toMatchObject({
      totalMinor: '0',
      taxMinor: '0',
      shippingMinor: '0',
      currency: 'JPY',
    });
    expect(actual.acquisition?.pricingState).toBe('KNOWN');
    expect(() => actualPlantInput({ ...d, price: '1.25' }, '2026-11-15')).toThrow('minor unit');
  });
  it('requires an actual placement only when the user chooses to record it', () => {
    const d = { ...gardenDraft(plan, 'USD'), placement: 'GROUND' };
    expect(actualPlantInput(d, '2026-11-15').initialContext).toBeNull();
    expect(
      actualPlantInput({ ...d, confirmContext: true }, '2026-11-15').initialContext?.placement,
    ).toBe('GROUND');
    expect(() =>
      actualPlantInput({ ...d, confirmContext: true, placement: '' }, '2026-11-15'),
    ).toThrow('actual current placement');
  });
  it('rejects impossible/future actual dates and exact amount mismatches', () => {
    const d = gardenDraft(plan, 'USD');
    expect(() => actualPlantInput({ ...d, acquiredOn: '2026-02-30' }, '2026-11-15')).toThrow(
      'Actual dates',
    );
    expect(() => actualPlantInput({ ...d, plantedOn: '2027-01-01' }, '2026-11-15')).toThrow(
      'Actual dates',
    );
    expect(() =>
      actualPlantInput({ ...d, price: '10', tax: '9', shipping: '2' }, '2026-11-15'),
    ).toThrow('cannot exceed');
  });
  it('records a confirmed sowing day with an unknown living group count', () => {
    const d = gardenDraft(plan, 'USD');
    const actual = actualPlantInput(d, '2026-11-15', '2026-11-14');
    expect(actual.type).toBe('GROUP');
    expect(actual.count).toBeNull();
    expect(actual.plantedOn).toBe('2026-11-14');
  });
});
