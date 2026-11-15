import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { type TodayOverviewQuery } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { SessionService } from '../../core/auth/session';
import { RainPanel } from './rain-panel';

const pending: TodayOverviewQuery['todayOverview']['rainReview'] = {
  status: 'READY',
  areaLabel: 'My garden',
  checkedAt: '2026-11-15T18:00:00Z',
  proposals: [
    {
      id: 'rain-review',
      day: '2026-11-15',
      estimatedAmountMm: '8',
      checkedAt: '2026-11-15T18:00:00Z',
    },
  ],
};
const ok = (data: unknown) => ({ kind: 'ok', errors: [], data });

describe('RainPanel', () => {
  const mutate = vi.fn();
  beforeEach(() => {
    mutate.mockReset();
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: GraphQLClient, useValue: { mutate } }],
    });
    TestBed.inject(SessionService).info.set({
      workspaceId: 'w',
      csrfToken: 'c',
      expiresAt: '2030-01-01T00:00:00Z',
    });
  });
  afterEach(() => {
    TestBed.resetTestingModule();
  });
  function render(review = pending) {
    const f = TestBed.createComponent(RainPanel);
    f.componentRef.setInput('review', review);
    f.detectChanges();
    return f;
  }

  it('keeps the model estimate out of the amount and saves an unknown amount explicitly', async () => {
    mutate.mockResolvedValue(ok({ recordRain: { rain: { id: 'rain' }, problems: [] } }));
    const f = render();
    f.componentInstance.open(pending.proposals[0]);
    f.detectChanges();
    expect(f.componentInstance.amount()).toBe('');
    expect(document.querySelector<HTMLInputElement>('#rain-review-amount')?.value).toBe('');
    await f.componentInstance.confirm();
    f.detectChanges();
    expect(mutate.mock.calls[0][1].input).toEqual({
      day: '2026-11-15',
      amountMm: null,
      rainReviewId: 'rain-review',
    });
    expect(f.componentInstance.proposals()).toHaveLength(0);
    expect(f.componentInstance.selected()).toBeNull();
  });

  it('converts an entered gauge depth exactly and reuses the key after an uncertain response', async () => {
    mutate
      .mockResolvedValueOnce({ kind: 'network' })
      .mockResolvedValueOnce(ok({ recordRain: { rain: { id: 'rain' }, problems: [] } }));
    const f = render();
    f.componentInstance.open(pending.proposals[0]);
    f.componentInstance.amount.set('0.5');
    await f.componentInstance.confirm();
    expect(f.componentInstance.amount()).toBe('0.5');
    expect(f.componentInstance.selected()).not.toBeNull();
    expect(f.componentInstance.error()).toContain('retrying is safe');
    await f.componentInstance.confirm();
    expect(mutate.mock.calls[0][1]).toEqual(mutate.mock.calls[1][1]);
    expect(mutate.mock.calls[1][1].input.amountMm).toBe('12.7');
  });

  it('rejects an invalid amount before sending a mutation', async () => {
    const f = render();
    f.componentInstance.open(pending.proposals[0]);
    f.componentInstance.amount.set('-1');
    await f.componentInstance.confirm();
    expect(mutate).not.toHaveBeenCalled();
    expect(f.componentInstance.error()).not.toBe('');
  });

  it('keeps a failed dismissal visible and hides it only after explicit success', async () => {
    mutate
      .mockResolvedValueOnce(
        ok({
          dismissRainReview: {
            dismissedId: null,
            problems: [{ message: 'Could not save response.' }],
          },
        }),
      )
      .mockResolvedValueOnce(
        ok({ dismissRainReview: { dismissedId: 'rain-review', problems: [] } }),
      );
    const f = render();
    await f.componentInstance.dismiss(pending.proposals[0]);
    expect(f.componentInstance.proposals()).toHaveLength(1);
    expect(f.componentInstance.error()).toContain('Could not save');
    await f.componentInstance.dismiss(pending.proposals[0]);
    expect(f.componentInstance.proposals()).toHaveLength(0);
    expect(mutate.mock.calls[0][1].clientMutationId).toBe(mutate.mock.calls[1][1].clientMutationId);
  });

  it('explains unconfigured, waiting and stale weather without claiming a dry day', () => {
    for (const [status, text] of [
      ['NOT_CONFIGURED', 'Set your garden coordinates'],
      ['WAITING', 'No weather check is available'],
      ['STALE', 'Weather checks are behind'],
    ] as const) {
      const f = render({ ...pending, status, proposals: [] });
      expect(f.nativeElement.textContent).toContain(text);
      expect(f.nativeElement.textContent).not.toContain('No rain today');
      f.destroy();
    }
  });
});
