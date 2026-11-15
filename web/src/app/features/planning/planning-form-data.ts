import type {
  CreatePlantingPlanInput,
  PlantingPeriodInput,
  PlantingSeason,
} from '@canopy/contracts';
import { blankToNull } from '../collection/shared';
import { toInput, parseAmount } from '../purchases/money';
import type { Plan } from './planning-data';

export interface PlanDraft {
  name: string;
  scientificName: string;
  cultivar: string;
  category: string;
  quantity: string;
  method: string;
  locationId: string;
  placement: string;
  notes: string;
  state: string;
  precision: string;
  fromDay: string;
  toDay: string;
  fromMonth: string;
  toMonth: string;
  season: string;
  year: string;
  budget: string;
  currency: string;
}
export function planDraft(plan: Plan | null, currency: string, idea = false): PlanDraft {
  const period = plan?.targetPeriod;
  return {
    name: plan?.name ?? '',
    scientificName: plan?.scientificName ?? '',
    cultivar: plan?.cultivar ?? '',
    category: plan?.category ?? 'OTHER',
    quantity: plan?.desiredQuantity?.toString() ?? '',
    method: plan?.method ?? '',
    locationId: plan?.intendedLocation?.id ?? '',
    placement: plan?.intendedPlacement ?? '',
    notes: plan?.notes ?? '',
    state: idea ? 'IDEA' : 'PLANNED',
    precision: period?.precision ?? '',
    fromDay: period?.dates?.from ?? '',
    toDay: period?.dates?.to ?? '',
    fromMonth: period?.months?.from ?? '',
    toMonth: period?.months?.to ?? '',
    season: period?.season?.season ?? 'SPRING',
    year: (period?.year ?? period?.season?.year)?.toString() ?? '',
    budget: plan?.budget ? toInput(plan.budget.minorUnits, plan.budget.currency) : '',
    currency: plan?.budget?.currency ?? currency,
  };
}
export function draftPeriod(
  d: Pick<
    PlanDraft,
    'precision' | 'fromDay' | 'toDay' | 'fromMonth' | 'toMonth' | 'season' | 'year'
  >,
): { period: PlantingPeriodInput | null; error?: string } {
  if (!d.precision) return { period: null };
  if (d.precision === 'DATE_RANGE') {
    if (!isCivilDate(d.fromDay) || !isCivilDate(d.toDay) || d.fromDay > d.toDay)
      return { period: null, error: 'Choose real start and end days in order.' };
    return { period: { precision: 'DATE_RANGE', dates: { from: d.fromDay, to: d.toDay } } };
  }
  if (d.precision === 'MONTH_RANGE') {
    if (
      ![d.fromMonth, d.toMonth].every((m) => /^(?!0000)\d{4}-(0[1-9]|1[0-2])$/.test(m)) ||
      d.fromMonth > d.toMonth
    )
      return { period: null, error: 'Choose start and end months in order.' };
    return { period: { precision: 'MONTH_RANGE', months: { from: d.fromMonth, to: d.toMonth } } };
  }
  if (!/^\d{1,4}$/.test(d.year) || +d.year < 1 || +d.year > 9999)
    return { period: null, error: 'Enter a year between 1 and 9999.' };
  if (d.precision === 'YEAR') return { period: { precision: 'YEAR', year: +d.year } };
  if (d.precision === 'SEASON' && ['SPRING', 'SUMMER', 'AUTUMN', 'WINTER'].includes(d.season))
    return {
      period: {
        precision: 'SEASON',
        season: { season: d.season as PlantingSeason, year: +d.year },
      },
    };
  return { period: null, error: 'Choose a supported period.' };
}
export function isCivilDate(value: string): boolean {
  if (!/^(?!0000)\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(value + 'T12:00:00Z');
  return !Number.isNaN(date.valueOf()) && date.toISOString().slice(0, 10) === value;
}
export function draftIntent(d: PlanDraft): {
  intent: Omit<CreatePlantingPlanInput, 'state'>;
  errors: Record<string, string>;
} {
  const errors: Record<string, string> = {};
  const name = d.name.trim();
  if (!name || [...name].length > 160) errors['name'] = 'Enter a name of 1–160 characters.';
  for (const key of ['scientificName', 'cultivar'] as const)
    if ([...d[key].trim()].length > 160) errors[key] = 'Keep this within 160 characters.';
  let desiredQuantity: number | null = null;
  if (d.quantity.trim()) {
    if (!/^\d+$/.test(d.quantity.trim()) || +d.quantity < 1 || +d.quantity > 2147483647)
      errors['quantity'] = 'Enter a positive whole quantity, or leave it unknown.';
    else desiredQuantity = +d.quantity;
  }
  const selected = draftPeriod(d);
  if (selected.error) errors['period'] = selected.error;
  const currency = d.currency.trim().toUpperCase();
  if (d.budget.trim() && !/^[A-Z]{3}$/.test(currency))
    errors['currency'] = 'Enter a three-letter currency code.';
  const amount = parseAmount(d.budget, currency, { required: false });
  if (d.budget.trim() && amount.error) errors['budget'] = amount.error;
  if ([...d.notes].length > 20000) errors['notes'] = 'Keep notes within 20,000 characters.';
  return {
    intent: {
      name,
      scientificName: blankToNull(d.scientificName),
      cultivar: blankToNull(d.cultivar),
      category: d.category as CreatePlantingPlanInput['category'],
      desiredQuantity,
      method: blankToNull(d.method) as CreatePlantingPlanInput['method'],
      intendedLocationId: blankToNull(d.locationId),
      intendedPlacement: blankToNull(d.placement) as CreatePlantingPlanInput['intendedPlacement'],
      targetPeriod: selected.period,
      budget:
        d.budget.trim() && !amount.error && amount.minor !== null
          ? { minorUnits: amount.minor!, currency }
          : null,
      notes: blankToNull(d.notes),
    },
    errors,
  };
}
