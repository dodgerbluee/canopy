import { TestBed } from '@angular/core/testing';
import { afterEach, describe, it, expect, vi } from 'vitest';
import { GraphQLClient } from '../../core/graphql/client';
import { PlanningAssistant } from './planning-assistant';
import { PlanningApi, type Plan, type Detail } from './planning-data';
const plan = { id: 'fig', name: 'Future fig', revision: 3 } as Plan;
const reply = {
  answer: 'Review the cited window.',
  isAI: true,
  planIds: ['fig'],
  sources: [
    {
      ref: 'p1f1',
      planId: 'fig',
      planName: 'Future fig',
      fact: {
        id: 'f',
        label: 'Window',
        summary: 'A verified fixture.',
        sourceTitle: 'Fixture source',
        sourceUrl: 'https://example.invalid/guide',
        publisher: 'Fixture',
        sourceRegion: 'Fixture',
        conditions: [],
        windows: [],
      },
    },
  ],
  note: { planId: 'fig', expectedRevision: 3, text: 'Proposed note' },
};
describe('planner assistant review', () => {
  afterEach(() => TestBed.resetTestingModule());
  function setup() {
    const mutate = vi.fn();
    const detail = vi.fn();
    TestBed.configureTestingModule({
      providers: [
        { provide: GraphQLClient, useValue: { mutate } },
        { provide: PlanningApi, useValue: { detail } },
      ],
    });
    const f = TestBed.createComponent(PlanningAssistant);
    f.componentRef.setInput('plans', [plan]);
    f.componentRef.setInput('open', true);
    f.detectChanges();
    return { f, c: f.componentInstance, mutate, detail };
  }
  it('makes no request before Ask and ignores an answer arriving after close', async () => {
    const { c, f, mutate } = setup();
    expect(mutate).not.toHaveBeenCalled();
    let resolve!: (v: unknown) => void;
    mutate.mockReturnValue(new Promise((r) => (resolve = r)));
    const asking = c.ask();
    f.componentRef.setInput('open', false);
    f.detectChanges();
    resolve({ kind: 'ok', errors: [], data: { askPlantingPlanner: reply } });
    await asking;
    expect(c.reply()).toBeNull();
    expect(mutate.mock.calls[0][2].aborted).toBe(true);
  });
  it('rechecks revision and refuses an old draft', async () => {
    const { c, detail } = setup();
    c.reply.set(reply as never);
    detail.mockResolvedValue({ ...plan, revision: 4 });
    const emit = vi.spyOn(c.reviewed, 'emit');
    await c.reviewNote();
    expect(emit).not.toHaveBeenCalled();
    expect(c.message()).toContain('changed');
  });
  it('emits a reviewed editor draft with source links without issuing a save', async () => {
    const { c, detail, mutate } = setup();
    c.reply.set(reply as never);
    detail.mockResolvedValue(plan as Detail);
    const emit = vi.spyOn(c.reviewed, 'emit');
    await c.reviewNote();
    expect(emit).toHaveBeenCalledWith({
      plan,
      text: 'Proposed note\n\nSources:\nhttps://example.invalid/guide',
      expectedRevision: 3,
    });
    expect(mutate).not.toHaveBeenCalled();
  });
  it('keeps a failed question and selected scope intact', async () => {
    const { c, mutate } = setup();
    c.question.set('When should I prepare?');
    mutate.mockResolvedValue({ kind: 'network' });
    await c.ask();
    expect(c.message()).toContain('reach');
    expect(c.question()).toBe('When should I prepare?');
    expect([...c.selected()]).toEqual(['fig']);
    expect(c.reply()).toBeNull();
  });
});
