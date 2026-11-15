import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PlanningApi } from './planning-data';
import { PlanEditor } from './plan-editor';

describe('planning modal form', () => {
  let api: { command: ReturnType<typeof vi.fn>; detail: ReturnType<typeof vi.fn> };
  beforeEach(() => {
    api = { command: vi.fn(), detail: vi.fn() };
    TestBed.configureTestingModule({ providers: [{ provide: PlanningApi, useValue: api }] });
  });
  afterEach(() => TestBed.resetTestingModule());
  async function render() {
    const fixture = TestBed.createComponent(PlanEditor);
    fixture.componentRef.setInput('open', true);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    return fixture;
  }
  it('does not show validation errors when first opened and never submits an empty name', async () => {
    const f = await render();
    expect(f.componentInstance.error('name')).toBe('');
    expect(document.querySelector('#plan-name-error')).toBeNull();
    await f.componentInstance.save();
    f.detectChanges();
    expect(f.componentInstance.error('name')).toContain('name');
    expect(api.command).not.toHaveBeenCalled();
  });
  it('clears busy after a failed request and keeps the same request key for a retry', async () => {
    const f = await render();
    f.componentInstance.draft.name = 'Future fig';
    api.command.mockRejectedValue(new Error('Connection lost'));
    await f.componentInstance.save();
    expect(f.componentInstance.saving()).toBe(false);
    expect(f.componentInstance.dirty()).toBe(true);
    await f.componentInstance.save();
    expect(api.command.mock.calls[0][1].clientMutationId).toBe(
      api.command.mock.calls[1][1].clientMutationId,
    );
    expect(api.command.mock.calls[0][1].input.budget).toBeNull();
  });
  it('starts a new intention when reopened rather than replaying another identical creation', async () => {
    const f = await render();
    f.componentInstance.draft.name = 'Future fig';
    api.command.mockRejectedValue(new Error('Offline'));
    await f.componentInstance.save();
    const key = api.command.mock.calls[0][1].clientMutationId;
    f.componentRef.setInput('open', false);
    f.detectChanges();
    await f.whenStable();
    f.componentRef.setInput('open', true);
    f.detectChanges();
    await f.whenStable();
    f.componentInstance.draft.name = 'Future fig';
    await f.componentInstance.save();
    expect(api.command.mock.calls[1][1].clientMutationId).not.toBe(key);
  });
  it('appends a reviewed note as a protected unsaved draft and keeps existing notes', async () => {
    const f = TestBed.createComponent(PlanEditor);
    f.componentRef.setInput('plan', {
      id: 'p',
      name: 'Fig',
      revision: 3,
      category: 'FIG',
      state: 'PLANNED',
      notes: 'My own note',
      actions: [],
    });
    f.componentRef.setInput('suggestedNote', { expectedRevision: 3, text: 'Reviewed suggestion' });
    f.componentRef.setInput('open', true);
    f.detectChanges();
    await f.whenStable();
    expect(f.componentInstance.draft.notes).toBe('My own note\n\nReviewed suggestion');
    expect(f.componentInstance.dirty()).toBe(true);
    expect(api.command).not.toHaveBeenCalled();
  });
});
