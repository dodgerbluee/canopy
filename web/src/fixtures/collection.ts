/* Synthetic fixture data for T05 visual references ONLY.
 * Photos are real and legally licensed (see public/assets/photos/LICENSES.json);
 * all names, dates, amounts and IDs are invented fixtures, never live data.
 * This file must not be imported by production feature code. */

export type StoryState =
  | 'ready'
  | 'loading'
  | 'empty'
  | 'error'
  | 'offline'
  | 'conflict'
  | 'session';

export const STORY_STATES: StoryState[] = [
  'ready',
  'loading',
  'empty',
  'error',
  'offline',
  'conflict',
  'session',
];

export interface PlantFixture {
  id: string;
  name: string;
  cultivar?: string;
  varietyKnown: boolean;
  photo: string;
  location: string;
  nextCheck: string;
  health: 'ok' | 'warn' | 'danger';
  healthLabel: string;
  archived?: boolean;
}

export const plants: PlantFixture[] = [
  {
    id: 'plnt-8f21',
    name: 'Monstera',
    cultivar: "'Deliciosa'",
    varietyKnown: true,
    photo: '/assets/photos/monstera.jpg',
    location: 'Living room · bright indirect',
    nextCheck: 'Water in 2 days',
    health: 'ok',
    healthLabel: 'Doing well',
  },
  {
    id: 'plnt-4c77',
    name: 'Echeveria',
    varietyKnown: false,
    photo: '/assets/photos/echeveria.jpg',
    location: 'Study · south window',
    nextCheck: 'Water in 9 days',
    health: 'ok',
    healthLabel: 'Doing well',
  },
  {
    id: 'plnt-9a30',
    name: 'Fiddle-leaf fig',
    cultivar: "'Brettle King'",
    varietyKnown: true,
    photo: '/assets/photos/ficus.png',
    location: 'Hallway · medium light',
    nextCheck: 'Check leaves today',
    health: 'warn',
    healthLabel: 'Watch: browning tips',
  },
  {
    id: 'plnt-1de5',
    name: 'Pothos',
    varietyKnown: true,
    photo: '/assets/photos/pothos.jpg',
    location: 'Bedroom · low light',
    nextCheck: 'Water in 4 days',
    health: 'ok',
    healthLabel: 'Doing well',
  },
  {
    id: 'plnt-6b02',
    name: 'Calathea',
    varietyKnown: true,
    photo: '/assets/photos/calathea.jpg',
    location: 'Bathroom · humid, medium',
    nextCheck: 'Mist today',
    health: 'danger',
    healthLabel: 'At risk: crispy edges',
  },
  {
    id: 'plnt-3ea9',
    name: 'Chinese money plant',
    cultivar: "'Compacta'",
    varietyKnown: true,
    photo: '/assets/photos/pilea.jpg',
    location: 'Study · east window',
    nextCheck: 'Rotate in 3 days',
    health: 'ok',
    healthLabel: 'Doing well',
  },
  {
    id: 'plnt-7c14',
    name: 'Golden barrel cactus',
    varietyKnown: true,
    photo: '/assets/photos/cactus.jpg',
    location: 'Balcony · full sun',
    nextCheck: 'Water in 21 days',
    health: 'ok',
    healthLabel: 'Doing well',
    archived: true,
  },
  {
    id: 'plnt-2f88',
    name: 'Boston fern',
    varietyKnown: true,
    photo: '/assets/photos/fern.jpg',
    location: 'Bathroom · humid, medium',
    nextCheck: 'Mist today',
    health: 'warn',
    healthLabel: 'Watch: frond tips',
  },
];

export interface JourneyEvent {
  kind: 'photo' | 'note' | 'measurement' | 'activity';
  date: string;
  title: string;
  detail?: string;
  photo?: string;
  sourceBadge?: string;
}

export const journeyGroups: { label: string; events: JourneyEvent[] }[] = [
  {
    label: 'October 2026',
    events: [
      {
        kind: 'photo',
        date: '2026-10-28',
        title: 'New leaf fully unfurled',
        photo: '/assets/photos/monstera.jpg',
        sourceBadge: 'Original date',
      },
      {
        kind: 'measurement',
        date: '2026-10-21',
        title: 'Height 84 cm',
        detail: 'Measured to tallest petiole · provenance plnt-8f21',
      },
      {
        kind: 'activity',
        date: '2026-10-19',
        title: 'Watered + rotated 90°',
        detail: 'Completed from care plan “Bright indirect, soak & drain”',
      },
      {
        kind: 'note',
        date: '2026-10-12',
        title: 'Repotted one size up',
        detail: 'Fresh aroid mix; roots circled the old pot.',
        sourceBadge: 'Edited from 2026-10-11',
      },
    ],
  },
  {
    label: 'September 2026',
    events: [
      {
        kind: 'photo',
        date: '2026-09-14',
        title: 'Two cataphylls drying out',
        photo: '/assets/photos/monstera2.jpg',
        detail: 'Different viewpoint than later photos.',
      },
      {
        kind: 'activity',
        date: '2026-09-07',
        title: 'Fed, half strength',
      },
    ],
  },
];

export const conditions = {
  place: 'Living room',
  summary: 'Mild, overcast',
  tempC: 21,
  humidity: 54,
  freshFor: 'updated 12 min ago',
  photo: '/assets/photos/monstera.jpg',
};

export interface CareEntry {
  group: 'overdue' | 'today' | 'upcoming';
  title: string;
  isCheck: boolean;
  plant: string;
  reason: string;
  source: string;
}

export const careEntries: CareEntry[] = [
  {
    group: 'overdue',
    title: 'Soil moisture check',
    isCheck: true,
    plant: 'Calathea',
    reason: 'Crispy edges since Oct 24; plan assumes even moisture.',
    source: 'Your plan · edited Oct 26',
  },
  {
    group: 'today',
    title: 'Water (soak & drain)',
    isCheck: false,
    plant: 'Monstera',
    reason: '7 days since last soak; top 3 cm dry.',
    source: 'Your plan “Bright indirect”',
  },
  {
    group: 'today',
    title: 'Inspect leaf tips',
    isCheck: true,
    plant: 'Fiddle-leaf fig',
    reason: 'Three brown tips logged Oct 27.',
    source: 'Journal-linked check',
  },
  {
    group: 'upcoming',
    title: 'Rotate 90°',
    isCheck: false,
    plant: 'Chinese money plant',
    reason: 'Even growth habit.',
    source: 'Your plan “East window”',
  },
];

export const purchase = {
  seller: 'Verdant Supply Co.',
  date: '2026-09-14',
  receipt: '/assets/photos/monstera2.jpg',
  lines: [
    {
      desc: 'Monstera deliciosa, 14" pot',
      amount: 42.0,
      allocated: ['Monstera plnt-8f21'],
      ocrDraft: false,
    },
    {
      desc: 'Aroid chunky mix, 8 L',
      amount: 14.5,
      allocated: ['Monstera plnt-8f21', 'Pothos plnt-1de5'],
      ocrDraft: true,
    },
    {
      desc: 'Ceramic saucer (assortment)',
      amount: 9.25,
      allocated: [],
      ocrDraft: true,
    },
  ],
  overhead: 4.99,
  unknownCostCount: 1,
};

export const insight = {
  title: 'Possible underwatering — Calathea',
  visible: 'Edges crisping on 3 of 9 leaves; no pests visible.',
  changed: 'First appeared between Oct 24 and Oct 29 photos.',
  explanations: ['Soil drying faster after bathroom heater use', 'Root bound'],
  missing: ['Last deep-water date uncertain (two journal gaps)', 'Pot size not recorded'],
  nextCheck: 'Soil moisture check today; photograph same leaf position.',
  citation: { publisher: 'Common houseplant reference', date: '2025-06', region: 'Indoor, temperate' },
  photoLeft: '/assets/photos/calathea.jpg',
  photoRight: '/assets/photos/fern.jpg',
};

export const captureQueue = [
  { name: 'IMG_2041.jpg', state: 'done', pct: 100, plant: 'Monstera', viewpoint: 'Whole plant' },
  { name: 'IMG_2042.jpg', state: 'processing', pct: 62, plant: 'Monstera', viewpoint: 'Leaf closeup' },
  { name: 'IMG_2043.jpg', state: 'failed', pct: 0, plant: '—', viewpoint: '—', error: 'Checksum mismatch — new reservation created; retry.' },
  { name: 'IMG_2044.jpg', state: 'queued', pct: 0, plant: '—', viewpoint: '—' },
];

export interface PurchaseFixture {
  id: string;
  seller: string | null;
  purchasedOn: string | null;
  currency: string;
  /** Pre-formatted exact display string — fixtures never do arithmetic. */
  total: string;
  lines: number;
  voided?: boolean;
}

export const purchases: PurchaseFixture[] = [
  { id: 'pur-01', seller: 'Stark Bros. Fig Nursery', purchasedOn: '2026-03-10', currency: 'USD', total: '100.00 USD', lines: 2 },
  { id: 'pur-02', seller: 'Jardinerie du Midi', purchasedOn: '2026-02-02', currency: 'EUR', total: '80.00 EUR', lines: 3 },
  { id: 'pur-03', seller: null, purchasedOn: '2026-01-18', currency: 'USD', total: '23.49 USD', lines: 1 },
  { id: 'pur-04', seller: 'Online marketplaces', purchasedOn: '2025-12-30', currency: 'USD', total: '55.00 USD', lines: 1, voided: true },
];

export const spendingSummary = {
  byCurrency: [
    { currency: 'USD', total: '123.49 USD' },
    { currency: 'EUR', total: '80.00 EUR' },
  ],
  unknownCostCount: 2,
  missingRecordCount: 1,
};

export const photoCredits =
  'Fixture photos: Wikimedia Commons contributors under CC BY / CC BY-SA licenses — see /assets/photos/LICENSES.json for per-image attribution.';
