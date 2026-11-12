import { describe, it, expect } from 'vitest';
import { seasonalSegments, laneCount, type SeasonalFact } from './seasons-data';
const fact = (id: string, months: number[]) => ({ id, months }) as SeasonalFact;
describe('seasonal tracks', () => {
  it('splits winter at the year boundary without inventing intermediate months', () => {
    const segments = seasonalSegments([fact('winter', [12, 1, 2])]);
    expect(segments.map((s) => [s.start, s.end])).toEqual([
      [1, 2],
      [12, 12],
    ]);
  });
  it('packs simultaneous bloom, growth and care windows into separate non-overlapping lanes', () => {
    const segments = seasonalSegments([
      fact('growth', [3, 4, 5, 6]),
      fact('bloom', [3, 4]),
      fact('fertilize', [4, 5]),
      fact('harvest', [7, 8]),
    ]);
    expect(laneCount(segments)).toBe(3);
    for (const a of segments)
      for (const b of segments) {
        if (a !== b && a.lane === b.lane) expect(a.end < b.start || b.end < a.start).toBe(true);
      }
  });
  it('leaves conditional guidance off the calendar, preserving discrete source months', () => {
    expect(seasonalSegments([fact('conditional', [])])).toEqual([]);
    expect(
      seasonalSegments([fact('split-feeding', [2, 5, 9])]).map((s) => [s.start, s.end]),
    ).toEqual([
      [2, 2],
      [5, 5],
      [9, 9],
    ]);
  });
  it('does not mutate input facts and rejects invalid/duplicate months', () => {
    const f = fact('x', [3, 3, 2, 0, 13, 2.5]);
    expect(seasonalSegments([f]).map((s) => [s.start, s.end])).toEqual([[2, 3]]);
    expect(f.months).toEqual([3, 3, 2, 0, 13, 2.5]);
  });
});
