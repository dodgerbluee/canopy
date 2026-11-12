import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, describe, it, expect, vi } from 'vitest';
import { GraphQLClient } from '../../core/graphql/client';
import { CareGuidance } from './care-guidance';
describe('care guidance separate from scheduled work', () => {
  afterEach(() => {
    TestBed.resetTestingModule();
    vi.useRealTimers();
  });
  it('uses the garden local month and keeps untimed condition checks outside the dated groups', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-11-01T00:30:00Z'));
    const query = vi.fn(async () => ({
      kind: 'ok', errors: [],
      data: {
        gardenKnowledge: {
          settings: { enabled: true, timezone: 'America/Chicago' },
          plants: [
            {
              plantId: 'fig',
              name: 'Fig',
              facts: [
                { id: 'feed', kind: 'FERTILIZE', months: [10] },
                { id: 'water', kind: 'WATER_CHECK', months: [] },
              ],
            },
          ],
          records: [],
        },
      },
    }));
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: GraphQLClient, useValue: { query } }],
    });
    const f = TestBed.createComponent(CareGuidance);
    await f.componentInstance.load();
    expect(f.componentInstance.month()).toBe(10);
    expect(f.componentInstance.monthName()).toBe('October');
    expect(f.componentInstance.groups().map((g) => g.kind)).toEqual(['FERTILIZE']);
    expect(f.componentInstance.checks().map((w) => w.fact.id)).toEqual(['water']);
  });
});
