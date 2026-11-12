import type { JournalQuery, PhotoDateSource } from '@canopy/contracts';

/** The generated union member type — the single source of truth for every
 * card the timeline renders. Exhaustiveness below is checked by the compiler
 * against this generated type, never a hand-copied list. */
export type JournalItem = JournalQuery['journal']['items'][number];
export type ActivityEntry = Extract<JournalItem, { __typename: 'ActivityEntry' }>;
export type MeasurementEntry = Extract<JournalItem, { __typename: 'MeasurementEntry' }>;

/** Compile-time exhaustiveness: adding a union member to the frozen SDL
 * breaks this switch at build time (the acceptance compile check). */
export function entryTypeLabel(entry: JournalItem): string {
  switch (entry.__typename) {
    case 'ActivityEntry':
      return 'activity';
    case 'MeasurementEntry':
      return 'measurement';
    case 'PhotoEntry':
      return 'photo';
    case 'PurchaseEntry':
      return 'purchase';
    case 'CareEntry':
      return 'care';
    case 'InsightEntry':
      return 'insight';
    default: {
      const _never: never = entry;
      return _never;
    }
  }
}

/** Only owner-editable entries get actions; photo/purchase editing lives in
 * their own features, and care entries are edited through their occurrence
 * mutations. Exhaustive over the same generated union. */
export function editableEntry(entry: JournalItem): ActivityEntry | MeasurementEntry | null {
  switch (entry.__typename) {
    case 'ActivityEntry':
      return entry;
    case 'MeasurementEntry':
      return entry;
    case 'PhotoEntry':
    case 'PurchaseEntry':
    case 'CareEntry':
    case 'InsightEntry':
      return null;
    default: {
      const _never: never = entry;
      return _never;
    }
  }
}

/** Photo date provenance is stated, never smoothed over: an upload-fallback
 * or missing takenAt must read differently from a known capture instant. */
export function dateSourceLabel(source: PhotoDateSource): string {
  switch (source) {
    case 'EXIF':
      return 'Original date';
    case 'USER':
      return 'Owner-stated date';
    case 'UPLOAD_FALLBACK':
      return 'Upload date — not capture date';
    default: {
      const _never: never = source;
      return _never;
    }
  }
}

export function monthLabel(key: string): string {
  return new Date(`${key}-01T00:00:00Z`).toLocaleDateString('en-US', {
    month: 'long',
    year: 'numeric',
    timeZone: 'UTC',
  });
}

export function dayLabel(iso: string): string {
  return new Date(iso).toLocaleDateString('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });
}

export function timeLabel(iso: string): string {
  return new Date(iso).toLocaleTimeString('en-US', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZone: 'UTC',
  });
}

/** Chronological month groups over the DESC-ordered page list. */
export function groupByMonth(items: JournalItem[]): { key: string; label: string; items: JournalItem[] }[] {
  const out: { key: string; label: string; items: JournalItem[] }[] = [];
  for (const it of items) {
    const key = it.occurredAt.slice(0, 7);
    const last = out[out.length - 1];
    if (last && last.key === key) last.items.push(it);
    else out.push({ key, label: monthLabel(key), items: [it] });
  }
  return out;
}

/** ── backdating helpers ───────────────────────────────────────────────────
 * The form collects a local calendar date + optional local time and converts
 * to an exact UTC instant; the browser's IANA zone travels with it so the
 * server records what the user actually meant. Blank time = local midnight,
 * an explicit choice, never an invented "now". */
export function localDateInput(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export function localTimeInput(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function localInstant(date: string, time: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null;
  const t = /^\d{2}:\d{2}$/.test(time) ? time : '00:00';
  const d = new Date(`${date}T${t}:00`);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

export function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}
