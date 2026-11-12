import { provideRouter } from '@angular/router';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { JournalPage } from './journal';
import { JournalTimeline } from './journal-timeline';
import { QuickLog } from './quick-log';

/** ── fixtures: exactly the shapes the generated Journal union carries ──── */
function activityRow(over: Record<string, unknown> = {}) {
  return {
    __typename: 'ActivityEntry',
    id: 'je-a1',
    plantId: 'pl-1',
    occurredAt: '2026-08-05T04:00:00Z',
    createdAt: '2026-08-05T04:00:00Z',
    summary: 'Watered',
    tombstonedAt: null,
    activity: {
      id: 'act-1',
      plantId: 'pl-1',
      kind: 'WATER',
      occurredAt: '2026-08-05T04:00:00Z',
      timezone: 'UTC',
      note: null,
      quantity: '500',
      unit: 'ML',
      product: null,
      revision: 2,
      voidedAt: null,
      createdAt: '2026-08-05T04:00:00Z',
      updatedAt: '2026-08-05T04:00:00Z',
    },
    ...over,
  };
}

function measurementRow(over: Record<string, unknown> = {}) {
  return {
    __typename: 'MeasurementEntry',
    id: 'je-m1',
    plantId: 'pl-1',
    occurredAt: '2026-10-21T12:00:00Z',
    createdAt: '2026-10-21T12:00:00Z',
    summary: 'Height 12.5 cm',
    tombstonedAt: null,
    measurement: {
      id: 'meas-1',
      plantId: 'pl-1',
      quantityKind: 'LENGTH',
      value: '12.5',
      unit: 'CM',
      observedAt: '2026-10-21T12:00:00Z',
      method: 'MANUAL',
      uncertainty: null,
      activityId: null,
      revision: 1,
      createdAt: '2026-10-21T12:00:00Z',
      updatedAt: '2026-10-21T12:00:00Z',
    },
    ...over,
  };
}

function photoRow(over: Record<string, unknown> = {}) {
  return {
    __typename: 'PhotoEntry',
    id: 'je-p1',
    plantId: 'pl-1',
    occurredAt: '2026-09-15T12:00:00Z',
    createdAt: '2026-09-15T12:00:00Z',
    summary: 'Photo added',
    tombstonedAt: null,
    photo: {
      id: 'ph-1',
      plantId: 'pl-1',
      takenAt: null,
      dateSource: 'UPLOAD_FALLBACK',
      timezone: null,
      caption: null,
      viewpoint: 'WHOLE',
      isCover: false,
      revision: 1,
      deletedAt: null,
      createdAt: '2026-09-15T12:00:00Z',
      updatedAt: '2026-09-15T12:00:00Z',
      asset: {
        id: 'as-1',
        purpose: 'PHOTO',
        state: 'READY',
        byteCount: 1234,
        mimeType: 'image/jpeg',
        sha256: 'ab'.repeat(32),
        displayName: null,
        errorCode: null,
        expiresAt: null,
        createdAt: '2026-09-15T12:00:00Z',
        updatedAt: '2026-09-15T12:00:00Z',
        variants: [{ name: 'thumb', widthPx: 200, heightPx: 150 }],
      },
    },
    ...over,
  };
}

function purchaseRow(over: Record<string, unknown> = {}) {
  return {
    __typename: 'PurchaseEntry',
    id: 'je-pu1',
    plantId: 'pl-1',
    occurredAt: '2026-09-14T00:00:00Z',
    createdAt: '2026-09-14T00:00:00Z',
    summary: 'Purchased',
    tombstonedAt: null,
    allocation: {
      id: 'alloc-1',
      purchaseLineId: 'line-1',
      plantId: 'pl-1',
      lineMinor: '4500',
      taxMinor: '250',
      shippingMinor: '250',
    },
    ...over,
  };
}

function journalEnvelope(items: unknown[], hasNext = false, endCursor: string | null = null) {
  return { journal: { items, pageInfo: { endCursor, hasNextPage: hasNext } } };
}

/** fetch stub routing by operation name; handlers may depend on variables. */
function stubFetch(handlers: Record<string, unknown>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as {
        query: string;
        variables?: object;
      };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      const vars = (body.variables ?? {}) as Record<string, unknown>;
      calls.push({ op, vars });
      let h = handlers[op];
      if (typeof h === 'function') h = (h as (v: Record<string, unknown>) => unknown)(vars);
      if (h === 'network') throw new TypeError('fetch failed');
      if (h === '401') return new Response('', { status: 401 });
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('JournalTimeline', () => {
  let session: SessionService;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1',
      expiresAt: '2026-12-01T00:00:00Z',
      csrfToken: 'c',
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  function render(plantId: string | null = 'pl-1') {
    const fixture = TestBed.createComponent(JournalTimeline);
    fixture.componentRef.setInput('plantId', plantId);
    fixture.detectChanges();
    return fixture;
  }

  it('opens a header watering entry with Water selected and unknown quantity left blank', () => {
    const fixture = TestBed.createComponent(QuickLog);
    fixture.componentRef.setInput('plantId', 'pl-1');
    fixture.componentRef.setInput('initialKind', 'WATER');
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    expect(root.querySelector<HTMLSelectElement>('[name="kind"]')?.value).toBe('WATER');
    expect(root.querySelector<HTMLInputElement>('[name="quantity"]')?.value).toBe('');
  });

  const dialog = (): HTMLElement => {
    const panels = [...document.querySelectorAll<HTMLElement>('.dialog-panel')];
    expect(panels, 'exactly one journal entry or confirmation dialog is open').toHaveLength(1);
    return panels[0];
  };
  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    (fixture.nativeElement.textContent ?? '') +
    [...document.querySelectorAll('.dialog-panel')]
      .map((panel) => panel.textContent ?? '')
      .join(' ');
  const buttons = (root: HTMLElement, label: string) =>
    [...root.querySelectorAll('button')].filter(
      (b) => b.textContent?.trim() === label || b.getAttribute('aria-label') === label,
    );
  const form = () => dialog().querySelector('form') as HTMLFormElement;
  const setField = (root: HTMLElement, label: string, value: string) => {
    const lab = [...root.querySelectorAll('label')].find((l) => l.textContent?.trim() === label);
    const input = lab
      ? (root.querySelector(`#${CSS.escape(lab.htmlFor)}`) as
          HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | null)
      : null;
    expect(input, `field labelled "${label}" exists`).toBeTruthy();
    input!.value = value;
    input!.dispatchEvent(new Event('change', { bubbles: true }));
  };
  const submit = async (fixture: { nativeElement: HTMLElement; detectChanges: () => void }) => {
    form().dispatchEvent(new Event('submit'));
    await flush();
    fixture.detectChanges();
  };

  it('renders typed cards for every union member without zero-filling unknowns', async () => {
    stubFetch({
      Journal: journalEnvelope([
        activityRow({
          activity: { ...activityRow().activity, quantity: null, unit: null },
        }),
        measurementRow(),
        photoRow(),
        purchaseRow(),
      ]),
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain('Watered');
    expect(text).toContain('Height 12.5 cm');
    expect(text).toContain('12.5 cm');
    expect(text).toContain('Photo added');
    // unknown capture instant is stated, never replaced by a plausible date
    expect(text).toContain('Date unknown');
    expect(text).toContain('Purchased');
    // money never rides on journal cards (spending lives on the purchases screen)
    expect(text).not.toContain('4500');
    expect(text).not.toContain('45.00');
    // absent optionals render nothing, not "null"/"0"/"±"
    expect(text).not.toContain('null');
    expect(text).not.toContain('±');
    expect(text).toContain('August 2026');
    expect(text).toContain('October 2026');
    expect(text).toContain('4 shown');
  });

  it('a failure is an explicit error card, never a fake empty timeline', async () => {
    stubFetch({ Journal: 'network' });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    const text = textOf(fixture);
    expect(text).toContain('Journal failed to load');
    expect(text).toContain('not an empty timeline');
    expect(text).not.toContain('No journal yet');
  });

  it('empty vs filtered-empty are different states', async () => {
    stubFetch({ Journal: journalEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('No journal yet');

    buttons(fixture.nativeElement, 'Filter entries')[0].click();
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    buttons(dialog(), 'Photos')[0].click();
    buttons(dialog(), 'Apply filters')[0].click();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('No matching entries');
  });

  it('modal type filter drives the frozen JournalFilter contract', async () => {
    const calls = stubFetch({ Journal: journalEnvelope([activityRow()]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Filter entries')[0].click();
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    buttons(dialog(), 'Activities')[0].click();
    buttons(dialog(), 'Apply filters')[0].click();
    await flush();
    const list = calls.filter((c) => c.op === 'Journal');
    expect(list[0]?.vars).toMatchObject({ first: 25, filter: {} });
    expect(list[list.length - 1]?.vars).toMatchObject({
      filter: { entryTypes: ['ACTIVITY'] },
    });
  });

  it('validates date ranges before applying them to the server filter', async () => {
    const calls = stubFetch({ Journal: journalEnvelope([activityRow()]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Filter entries')[0].click();
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    const set = (id: string, value: string) => {
      const input = dialog().querySelector<HTMLInputElement>('#' + id)!;
      input.value = value;
      input.dispatchEvent(new Event('input', { bubbles: true }));
    };
    set('journey-from', '2026-11-05T12:00');
    set('journey-until', '2026-11-01T12:00');
    buttons(dialog(), 'Apply filters')[0].click();
    fixture.detectChanges();
    await flush();
    expect(dialog().textContent).toContain('valid range');
    expect(calls.filter((c) => c.op === 'Journal')).toHaveLength(1);
    set('journey-until', '2026-11-10T12:00');
    buttons(dialog(), 'Apply filters')[0].click();
    fixture.detectChanges();
    await flush();
    expect(calls.filter((c) => c.op === 'Journal').at(-1)?.vars).toMatchObject({
      filter: {
        from: new Date('2026-11-05T12:00').toISOString(),
        to: new Date('2026-11-10T12:00').toISOString(),
      },
    });
  });

  it('load-more appends pages; a post-mutation refresh replaces them with no duplicate ids', async () => {
    const page1 = journalEnvelope([activityRow(), measurementRow()], true, 'c1');
    const page2 = journalEnvelope([photoRow()]);
    let voided = false;
    const calls = stubFetch({
      Journal: (vars: Record<string, unknown>) =>
        voided ? journalEnvelope([measurementRow(), photoRow()]) : vars['after'] ? page2 : page1,
      VoidActivity: () => {
        voided = true;
        return {
          voidActivity: {
            clientMutationId: null,
            activity: null,
            problems: [],
          },
        };
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('2 shown');

    buttons(fixture.nativeElement, 'Load earlier')[0].click();
    await flush();
    fixture.detectChanges();
    expect(textOf(fixture)).toContain('3 shown');
    const ids = () =>
      [...fixture.nativeElement.querySelectorAll('li.card')].map((li) => li.textContent ?? '');
    expect(ids().length).toBe(3);

    // void the activity → refresh from page one; the voided entry is gone
    buttons(fixture.nativeElement, 'Void')[0].click();
    fixture.detectChanges();
    buttons(dialog(), 'Void activity')[0].click();
    await flush();
    fixture.detectChanges();
    const voidCall = calls.find((c) => c.op === 'VoidActivity');
    expect(voidCall?.vars).toMatchObject({
      input: { id: 'act-1', expectedRevision: 2 },
    });
    const texts = ids();
    expect(texts.length).toBe(2); // replaced, not appended — no duplicates
    expect(new Set(texts.map((t) => t.slice(0, 40))).size).toBe(texts.length);
    expect(texts.join(' ')).not.toContain('Watered');
    expect(texts.join(' ')).toContain('Photo added');
  });

  it('measurement delete is two-step and revision-guarded', async () => {
    const calls = stubFetch({
      Journal: journalEnvelope([measurementRow()]),
      DeleteMeasurement: {
        deleteMeasurement: {
          clientMutationId: null,
          measurementId: 'meas-1',
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Delete')[0].click();
    fixture.detectChanges();
    buttons(dialog(), 'Delete measurement')[0].click();
    await flush();
    const del = calls.find((c) => c.op === 'DeleteMeasurement');
    expect(del?.vars).toMatchObject({
      input: { id: 'meas-1', expectedRevision: 1 },
    });
  });

  it('quick log records a backdated water entry with exact variables', async () => {
    const calls = stubFetch({
      Journal: journalEnvelope([]),
      RecordActivity: {
        recordActivity: {
          clientMutationId: null,
          activity: activityRow().activity,
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Quick log')[0].click();
    fixture.detectChanges();
    const root = dialog();
    setField(root, 'What happened', 'WATER');
    fixture.detectChanges();
    setField(root, 'Date', '2026-08-05');
    setField(root, 'Quantity', '500');
    setField(root, 'Unit', 'ML');
    await submit(fixture);
    const rec = calls.find((c) => c.op === 'RecordActivity');
    expect(rec?.vars).toMatchObject({
      input: {
        plantId: 'pl-1',
        kind: 'WATER',
        // blank time = local midnight of the chosen day, an explicit choice
        occurredAt: new Date('2026-08-05T00:00:00').toISOString(),
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        note: null,
        quantity: '500',
        unit: 'ML',
        product: null,
      },
    });
    // the timeline reloaded after the log
    expect(calls.filter((c) => c.op === 'Journal').length).toBeGreaterThan(1);
  });

  it('quick log records a measurement with kind/unit/value/method', async () => {
    const calls = stubFetch({
      Journal: journalEnvelope([]),
      RecordMeasurement: {
        recordMeasurement: {
          clientMutationId: null,
          measurement: measurementRow().measurement,
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Quick log')[0].click();
    fixture.detectChanges();
    buttons(dialog(), 'Measurement')[0].click();
    fixture.detectChanges();
    const root = dialog();
    setField(root, 'Value', '12.5');
    setField(root, 'Observed on', '2026-10-21');
    await submit(fixture);
    const rec = calls.find((c) => c.op === 'RecordMeasurement');
    expect(rec?.vars).toMatchObject({
      input: {
        plantId: 'pl-1',
        quantityKind: 'LENGTH',
        unit: 'CM',
        value: '12.5',
        observedAt: new Date('2026-10-21T00:00:00').toISOString(),
        method: 'MANUAL',
        uncertainty: null,
      },
    });
  });

  it('switching entry modes preserves each draft without recording either', async () => {
    const calls = stubFetch({ Journal: journalEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Quick log')[0].click();
    fixture.detectChanges();
    setField(dialog(), 'Note', 'Leaves look brighter today');
    setField(dialog(), 'Date', '2026-08-05');

    const switchTo = async (label: string) => {
      buttons(dialog(), label)[0].click();
      fixture.detectChanges();
      await fixture.whenStable();
    };
    await switchTo('Measurement');
    setField(dialog(), 'Value', '12.5');
    await switchTo('Activity');
    expect((dialog().querySelector('#ql-note') as HTMLTextAreaElement).value).toBe(
      'Leaves look brighter today',
    );
    expect((dialog().querySelector('#ql-date') as HTMLInputElement).value).toBe('2026-08-05');
    await switchTo('Measurement');
    expect((dialog().querySelector('#ql-value') as HTMLInputElement).value).toBe('12.5');
    expect(
      calls.some((call) => call.op === 'RecordActivity' || call.op === 'RecordMeasurement'),
    ).toBe(false);
  });

  it('quantity without a unit is refused client-side before any mutation', async () => {
    const calls = stubFetch({ Journal: journalEnvelope([]) });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Quick log')[0].click();
    fixture.detectChanges();
    const root = dialog();
    setField(root, 'What happened', 'WATER');
    fixture.detectChanges();
    setField(root, 'Date', '2026-08-05');
    setField(root, 'Quantity', '500');
    await submit(fixture);
    expect(calls.some((c) => c.op === 'RecordActivity')).toBe(false);
    expect(textOf(fixture)).toContain('Quantity needs a unit');
  });

  it('edit prefills from the entry and sends expectedRevision', async () => {
    const calls = stubFetch({
      Journal: journalEnvelope([activityRow()]),
      UpdateActivity: {
        updateActivity: {
          clientMutationId: null,
          activity: activityRow().activity,
          problems: [],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Edit')[0].click();
    fixture.detectChanges();
    const root = dialog();
    // prefill: kind frozen, date/quantity/unit restored from the entry
    const kindSel = [...root.querySelectorAll('label')].find(
      (l) => l.textContent?.trim() === 'What happened',
    );
    const kindEl = root.querySelector(`#${CSS.escape(kindSel!.htmlFor)}`) as HTMLSelectElement;
    expect(kindEl.disabled).toBe(true);
    expect(kindEl.value).toBe('WATER');
    const dateEl = root.querySelector('#ql-date') as HTMLInputElement;
    expect(dateEl.value).toBe('2026-08-05');
    setField(root, 'Note', 'soak and drain');
    await submit(fixture);
    const upd = calls.find((c) => c.op === 'UpdateActivity');
    expect(upd?.vars).toMatchObject({
      input: {
        id: 'act-1',
        expectedRevision: 2,
        occurredAt: new Date('2026-08-05T04:00:00').toISOString(), // local prefill round-trip
        note: 'soak and drain',
        quantity: '500',
        unit: 'ML',
        product: null,
      },
    });
  });

  it('a CONFLICT on edit reloads the timeline instead of overwriting', async () => {
    const calls = stubFetch({
      Journal: journalEnvelope([activityRow()]),
      UpdateActivity: {
        updateActivity: {
          clientMutationId: null,
          activity: null,
          problems: [
            {
              code: 'CONFLICT',
              field: 'input.expectedRevision',
              message: 'revision mismatch',
              currentRevision: 4,
            },
          ],
        },
      },
    });
    const fixture = render();
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Edit')[0].click();
    fixture.detectChanges();
    await submit(fixture);
    // conflict: explained via toast (rendered by the shell region) and the
    // timeline reloads the current state rather than keeping a stale form
    expect(calls.filter((c) => c.op === 'Journal').length).toBeGreaterThan(1);
    expect(document.querySelector('quick-log')).toBeNull();
  });

  it('workspace timeline offers a plant picker and refuses entries without one', async () => {
    const calls = stubFetch({
      Journal: journalEnvelope([]),
      Plants: {
        plants: {
          items: [{ id: 'pl-9', name: 'Pothos' }],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
    });
    const fixture = render(null);
    await flush();
    fixture.detectChanges();
    buttons(fixture.nativeElement, 'Quick log')[0].click();
    fixture.detectChanges();
    await flush();
    expect(calls.some((c) => c.op === 'Plants')).toBe(true);
    const root = dialog();
    setField(root, 'Date', '2026-08-05');
    await submit(fixture);
    expect(calls.some((c) => c.op === 'RecordActivity')).toBe(false);
    expect(textOf(fixture)).toContain('Choose the plant');
  });
});

describe('JournalPage', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideRouter([])] });
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1',
      expiresAt: '2026-12-01T00:00:00Z',
      csrfToken: 'c',
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('renders the workspace timeline shell', async () => {
    stubFetch({
      Journal: journalEnvelope([]),
      Plants: {
        plants: {
          items: [],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
    });
    const fixture = TestBed.createComponent(JournalPage);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent ?? '';
    expect(text).toContain('Journal');
    expect(
      (fixture.nativeElement as HTMLElement).querySelector('[aria-label="Quick log"]'),
    ).not.toBeNull();
    expect(text).toContain('No journal yet');
  });
});
