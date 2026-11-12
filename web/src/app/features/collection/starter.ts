import type { PlantCategory, PlantType } from '@canopy/contracts';

/**
 * The opt-in starter collection (T10 acceptance): five trees and two berry
 * groups. Nothing is seeded without an explicit opt-in, and berry groups
 * keep count unknown — the SDL permits a null count exactly for these.
 */
export interface StarterPlant {
  slug: string;
  name: string;
  category: PlantCategory;
  type: PlantType;
  count: number | null;
  cultivar: string | null;
}

export const STARTER_PLANTS: StarterPlant[] = [
  { slug: 'meyer-lemon', name: 'Meyer lemon', category: 'CITRUS', type: 'INDIVIDUAL', count: null, cultivar: 'Meyer' },
  { slug: 'arbequina-olive', name: 'Arbequina olive', category: 'OTHER', type: 'INDIVIDUAL', count: null, cultivar: 'Arbequina' },
  { slug: 'brown-turkey-fig', name: 'Brown Turkey fig', category: 'FIG', type: 'INDIVIDUAL', count: null, cultivar: 'Brown Turkey' },
  { slug: 'red-mulberry', name: 'Red mulberry', category: 'OTHER', type: 'INDIVIDUAL', count: null, cultivar: null },
  { slug: 'hass-avocado', name: 'Hass avocado', category: 'OTHER', type: 'INDIVIDUAL', count: null, cultivar: 'Hass' },
  { slug: 'alpine-strawberries', name: 'Alpine strawberries', category: 'STRAWBERRY', type: 'GROUP', count: null, cultivar: null },
  { slug: 'thornless-blackberries', name: 'Thornless blackberries', category: 'BLACKBERRY', type: 'GROUP', count: null, cultivar: null },
];

/** Stable idempotency keys: a retried opt-in replays instead of duplicating. */
export function starterKey(s: StarterPlant): string {
  return `starter-${s.slug}`;
}
