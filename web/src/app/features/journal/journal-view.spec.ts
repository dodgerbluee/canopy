import { describe, expect, it } from 'vitest';
import {
  dateSourceLabel,
  editableEntry,
  entryTypeLabel,
  groupByMonth,
  localDateInput,
  localInstant,
  localTimeInput,
  monthLabel,
  type JournalItem,
} from './journal-view';

/** Minimal shaped stubs — the compile-time exhaustiveness in journal-view is
 * enforced against the real generated union by tsc; these casts only feed
 * runtime grouping. */
const item = (typename: string, occurredAt: string) =>
  ({ __typename: typename, occurredAt }) as unknown as JournalItem;

describe('journal view helpers', () => {
  it('labels every generated union member (exhaustiveness surface)', () => {
    expect(entryTypeLabel({ __typename: 'ActivityEntry' } as JournalItem)).toBe('activity');
    expect(entryTypeLabel({ __typename: 'MeasurementEntry' } as JournalItem)).toBe('measurement');
    expect(entryTypeLabel({ __typename: 'PhotoEntry' } as JournalItem)).toBe('photo');
    expect(entryTypeLabel({ __typename: 'PurchaseEntry' } as JournalItem)).toBe('purchase');
  });

  it('only activities and measurements are owner-editable inline', () => {
    const act = { __typename: 'ActivityEntry', activity: { revision: 1 } } as unknown as JournalItem;
    const mea = { __typename: 'MeasurementEntry', measurement: {} } as unknown as JournalItem;
    expect(editableEntry(act)).toBe(act);
    expect(editableEntry(mea)).toBe(mea);
    expect(editableEntry({ __typename: 'PhotoEntry' } as JournalItem)).toBeNull();
    expect(editableEntry({ __typename: 'PurchaseEntry' } as JournalItem)).toBeNull();
  });

  it('photo date provenance is stated, never smoothed over', () => {
    expect(dateSourceLabel('EXIF')).toBe('Original date');
    expect(dateSourceLabel('USER')).toBe('Owner-stated date');
    expect(dateSourceLabel('UPLOAD_FALLBACK')).toBe('Upload date — not capture date');
  });

  it('groups a DESC page into chronological month sections', () => {
    const groups = groupByMonth([
      item('ActivityEntry', '2026-10-28T04:00:00Z'),
      item('MeasurementEntry', '2026-10-01T00:00:00Z'),
      item('PhotoEntry', '2026-09-15T12:00:00Z'),
    ]);
    expect(groups.map((g) => g.label)).toEqual(['October 2026', 'September 2026']);
    expect(groups[0].items.length).toBe(2);
    expect(monthLabel('2026-01')).toBe('January 2026');
  });

  it('backdating: blank time means local midnight, never an invented now', () => {
    const iso = localInstant('2026-08-05', '');
    expect(iso).toBe(new Date('2026-08-05T00:00:00').toISOString());
    expect(localInstant('2026-08-05', '14:30')).toBe(new Date('2026-08-05T14:30:00').toISOString());
    expect(localInstant('not-a-date', '')).toBeNull();
    expect(localInstant('', '')).toBeNull();
  });

  it('local date/time inputs round-trip an instant', () => {
    const iso = new Date('2026-08-05T14:30:00').toISOString();
    expect(localDateInput(iso)).toBe('2026-08-05');
    expect(localTimeInput(iso)).toBe('14:30');
  });
});
