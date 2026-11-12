import { type GardenKnowledgeQuery, type SeasonalFactFieldsFragment } from '@canopy/contracts';
export type Garden = GardenKnowledgeQuery['gardenKnowledge'];
export type GardenPlant = Garden['plants'][number];
export type SeasonalFact = SeasonalFactFieldsFragment;
export interface Segment {
  fact: SeasonalFact;
  start: number;
  end: number;
  lane: number;
}
/** Pack overlapping source windows into separate lanes; December–February
 * becomes two bands rather than crossing into an unrelated year. */
export function seasonalSegments(facts: SeasonalFact[]): Segment[] {
  const segments: Segment[] = [];
  for (const fact of facts) {
    const months = [...new Set(fact.months)]
      .filter((m) => Number.isInteger(m) && m >= 1 && m <= 12)
      .sort((a, b) => a - b);
    for (let i = 0; i < months.length;) {
      const start = months[i++];
      let end = start;
      while (i < months.length && months[i] === end + 1) end = months[i++];
      segments.push({ fact, start, end, lane: 0 });
    }
  }
  segments.sort((a, b) => a.start - b.start || b.end - a.end || a.fact.id.localeCompare(b.fact.id));
  const lanes: number[] = [];
  for (const segment of segments) {
    let lane = lanes.findIndex((end) => end < segment.start);
    if (lane < 0) lane = lanes.length;
    segment.lane = lane;
    lanes[lane] = segment.end;
  }
  return segments;
}
export function laneCount(segments: Segment[]): number {
  return Math.max(2, ...segments.map((s) => s.lane + 1));
}
export function requestKey(): string {
  return (
    'seasons-' +
    Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
      b.toString(16).padStart(2, '0'),
    ).join('')
  );
}

export const seasonalKinds = [
  { kind: 'GROWTH', label: 'Growth', icon: 'sprout' },
  { kind: 'BLOOM', label: 'Bloom', icon: 'flower' },
  { kind: 'HARVEST', label: 'Harvest', icon: 'fruit' },
  { kind: 'FERTILIZE', label: 'Fertilize', icon: 'sparkles' },
  { kind: 'WATER_CHECK', label: 'Moisture checks', icon: 'drop' },
  { kind: 'PRUNE', label: 'Prune', icon: 'prune' },
  { kind: 'DORMANCY', label: 'Dormancy', icon: 'moon' },
  { kind: 'PROTECT', label: 'Protection', icon: 'shield' },
] as const;

export interface GardenWindow {
  plant: GardenPlant;
  fact: SeasonalFact;
}
export interface GardenMonthGroup {
  kind: (typeof seasonalKinds)[number]['kind'];
  label: string;
  icon: (typeof seasonalKinds)[number]['icon'];
  plants: GardenPlant[];
  windows: GardenWindow[];
}
const careKinds = new Set<string>(['FERTILIZE', 'WATER_CHECK', 'PRUNE', 'PROTECT', 'HARVEST']);
/** Merge only explicitly supported months. Count plants once per kind/month even
 * when several source notes describe the same plant's care window. */
export function gardenMonths(plants: GardenPlant[], visible: ReadonlySet<string>) {
  return Array.from({ length: 12 }, (_, index) => {
    const month = index + 1;
    const groups: GardenMonthGroup[] = [];
    for (const kind of seasonalKinds) {
      if (!visible.has(kind.kind)) continue;
      const windows: GardenWindow[] = [];
      const matched = new Map<string, GardenPlant>();
      const seen = new Set<string>();
      for (const plant of plants) {
        for (const fact of plant.facts) {
          const key = JSON.stringify([plant.plantId, fact.id]);
          if (fact.kind !== kind.kind || !fact.months.includes(month) || seen.has(key)) continue;
          seen.add(key);
          windows.push({ plant, fact });
          matched.set(plant.plantId, plant);
        }
      }
      if (windows.length) groups.push({ ...kind, windows, plants: [...matched.values()] });
    }
    const carePlants = new Set(
      groups.filter((g) => careKinds.has(g.kind)).flatMap((g) => g.plants.map((p) => p.plantId)),
    );
    groups.sort((a, b) => Number(careKinds.has(b.kind)) - Number(careKinds.has(a.kind)));
    return { month, groups, carePlants: carePlants.size };
  });
}
