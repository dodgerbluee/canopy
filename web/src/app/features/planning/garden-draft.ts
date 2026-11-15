import type {
  CreatePlantInput,
  ContextPlacement,
  PlantCategory,
  PlantType,
} from '@canopy/contracts';
import { blankToNull, parseCount } from '../collection/shared';
import { parseAmount } from '../purchases/money';
import { isCivilDate } from './planning-form-data';
import type { Plan } from './planning-data';
export function gardenDraft(plan: Plan | null, currency: string) {
  return {
    mode: 'new',
    existingId: '',
    name: plan?.name ?? '',
    scientificName: plan?.scientificName ?? '',
    cultivar: plan?.cultivar ?? '',
    type: plan?.desiredQuantity && plan.desiredQuantity > 1 ? 'GROUP' : 'INDIVIDUAL',
    category: plan?.category ?? 'OTHER',
    count: '',
    plantedOn: '',
    acquiredOn: '',
    seller: '',
    confirmContext: false,
    placement: '',
    locationId: '',
    price: '',
    currency,
    tax: '0',
    shipping: '0',
    completedOn: '',
  };
}
export type GardenDraft = ReturnType<typeof gardenDraft>;
export function actualPlantInput(
  d: GardenDraft,
  today: string,
  plantingOn: string | null = null,
): CreatePlantInput {
  if (!d.name.trim() || [...d.name.trim()].length > 160)
    throw new Error('Enter the actual plant name (up to 160 characters).');
  const count = parseCount(d.count);
  if (count.error) throw new Error(count.error);
  if (count.value !== null && count.value > 2147483647) throw new Error('Count is too large.');
  const plantedOn = plantingOn || blankToNull(d.plantedOn);
  const acquiredOn = blankToNull(d.acquiredOn);
  for (const date of [plantedOn, acquiredOn])
    if (date && (!isCivilDate(date) || date > today))
      throw new Error('Actual dates must be on or before today in your garden.');
  if (d.confirmContext && !['CONTAINER', 'GROUND'].includes(d.placement))
    throw new Error('Choose the actual current placement.');
  const known = d.price.trim() !== '';
  const currency = d.currency.trim().toUpperCase();
  const price = parseAmount(d.price, currency);
  const tax = parseAmount(d.tax, currency);
  const shipping = parseAmount(d.shipping, currency);
  if (known) {
    if (!/^[A-Z]{3}$/.test(currency)) throw new Error('Enter a three-letter currency code.');
    for (const amount of [price, tax, shipping]) if (amount.error) throw new Error(amount.error);
    if (BigInt(tax.minor) + BigInt(shipping.minor) > BigInt(price.minor))
      throw new Error('Tax and shipping cannot exceed the total paid.');
  }
  const seller = blankToNull(d.seller);
  return {
    name: d.name.trim(),
    scientificName: blankToNull(d.scientificName),
    cultivar: blankToNull(d.cultivar),
    type: d.type as PlantType,
    category: d.category as PlantCategory,
    count: count.value,
    plantedOn,
    initialContext: d.confirmContext
      ? { placement: d.placement as ContextPlacement, locationId: blankToNull(d.locationId) }
      : null,
    acquisition:
      known || seller || acquiredOn
        ? { seller, acquiredOn, pricingState: known ? 'KNOWN' : 'UNKNOWN' }
        : null,
    initialPurchase: known
      ? {
          currency,
          totalMinor: price.minor,
          taxMinor: tax.minor,
          shippingMinor: shipping.minor,
          purchasedOn: acquiredOn,
          seller,
        }
      : null,
  };
}
