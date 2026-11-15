import { TestBed } from '@angular/core/testing';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { ActionEditor } from './action-editor';
import { PlanningApi, type Plan } from './planning-data';
describe('optional planning reminders', () => {
  afterEach(() => TestBed.resetTestingModule());
  function setup() {
    const command = vi.fn().mockResolvedValue({ plan: { id: 'p' }, problems: [] });
    TestBed.configureTestingModule({
      providers: [{ provide: PlanningApi, useValue: { command } }],
    });
    const f = TestBed.createComponent(ActionEditor);
    f.componentRef.setInput('plan', { id: 'p', revision: 3 } as Plan);
    f.componentRef.setInput('open', true);
    f.detectChanges();
    return { f, command, c: f.componentInstance };
  }
  it('starts with reminders off, then requires an explicit date no later than the action', async () => {
    const { c, command } = setup();
    expect(c.draft.remind).toBe(false);
    expect(c.message()).toBe('');
    c.draft = {
      ...c.draft,
      title: 'Sow',
      kind: 'DIRECT_SOW',
      day: '2027-03-14',
      remind: true,
      reminder: '2027-03-15',
    };
    await c.save();
    expect(command).not.toHaveBeenCalled();
    expect(c.message()).toContain('on or before');
    c.draft.reminder = '2027-03-13';
    await c.save();
    expect(command.mock.calls[0][1].input.reminderOn).toBe('2027-03-13');
  });
  it('sends null when opt-in is disabled even if a former date remains in the draft', async () => {
    const { c, command } = setup();
    c.draft = {
      ...c.draft,
      title: 'Buy seeds',
      day: '2027-03-14',
      reminder: '2027-03-13',
      remind: false,
    };
    await c.save();
    expect(command.mock.calls[0][1].input.reminderOn).toBeNull();
  });
});
