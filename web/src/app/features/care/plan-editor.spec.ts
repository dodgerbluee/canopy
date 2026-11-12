import { ActivatedRoute, Router, provideRouter } from '@angular/router';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { CarePlanEditor } from './plan-editor';

/** ── fixtures: exactly the shapes the frozen care fragments carry ─────── */
function rule(ruleKey: string, over: Record<string, unknown> = {}) {
  return {
    ruleKey,
    kind: 'ACTION',
    activityKind: 'WATER',
    recurrence: {
      __typename: 'EveryNDaysRecurrence',
      days: 7,
      anchor: 'PLAN_START',
    },
    requiredFacts: [],
    weatherSensitivity: 'NONE',
    guidanceText: 'Soak and drain when the top 3 cm are dry.',
    sourceVersions: [
      {
        id: 'sv-1',
        sourceId: 'src-1',
        citation: 'House Plant Care, Orchard Press, 2023',
        summary: 'Regional guidance.',
        retrievedOn: '2026-01-15',
        contentSha256: 'ab'.repeat(32),
        createdAt: '2026-01-15T00:00:00Z',
      },
    ],
    ...over,
  };
}

function profileVersion(version: number, rules: ReturnType<typeof rule>[], id = `pv-${version}`) {
  return {
    id,
    profileId: 'pr-1',
    version,
    status: 'REVIEWED',
    reviewerLabel: 'Reviewed by A. Green',
    reviewedOn: '2026-02-01',
    publishedAt: '2026-02-02T00:00:00Z',
    retiredAt: null,
    createdAt: '2026-02-02T00:00:00Z',
    environment: { situations: ['INDOOR'], hemispheres: ['NORTHERN'] },
    rules,
  };
}

function carePlan(over: Record<string, unknown> = {}) {
  return {
    id: 'cp-1',
    plantId: 'pl-1',
    profileVersion: profileVersion(2, [rule('water'), rule('mist', { activityKind: 'MIST' })]),
    timezone: 'UTC',
    enabled: true,
    notificationTime: '09:00',
    overrides: [{ ruleKey: 'mist', enabled: false, recurrenceOverride: null }],
    revision: 4,
    createdAt: '2026-09-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    ...over,
  };
}

function plantDetail() {
  return {
    plant: {
      id: 'pl-1',
      name: 'Monstera',
      scientificName: null,
      cultivar: null,
      type: 'POTTED',
      category: 'HOUSEPLANT',
      status: 'ALIVE',
      revision: 1,
      photoCount: 0,
      tags: [],
      currentContext: null,
    },
  };
}

/** The plan now rides its own self-contained read (care.graphql). */
function plantCarePlan(plan: Record<string, unknown> | null) {
  return { plant: { id: 'pl-1', carePlan: plan } };
}

function stubFetch(handlers: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as {
        query: string;
        variables?: object;
      };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      let h = handlers[op];
      if (typeof h === 'function') h = (h as (v: object) => unknown)(body.variables ?? {});
      if (h === 'network') throw new TypeError('fetch failed');
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
}

/** fetch calls decoded to {op, vars} for assertions. */
function gqlCalls(): { op: string; vars: Record<string, unknown> }[] {
  return (fetch as ReturnType<typeof vi.fn>).mock.calls.map((c: unknown[]) => {
    const body = JSON.parse(String((c[1] as RequestInit).body)) as {
      query: string;
      variables?: object;
    };
    return {
      op: /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '',
      vars: (body.variables ?? {}) as Record<string, unknown>,
    };
  });
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('CarePlanEditor', () => {
  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        {
          provide: ActivatedRoute,
          useValue: { snapshot: { paramMap: new Map([['plantId', 'pl-1']]) } },
        },
      ],
    });
    vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1',
      expiresAt: '2026-12-01T00:00:00Z',
      csrfToken: 'c',
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    // the entry dialog renders in a CDK overlay on document.body
    document.body.innerHTML = '';
  });

  function render(handlers: Record<string, unknown>) {
    stubFetch(handlers);
    const fixture = TestBed.createComponent(CarePlanEditor);
    fixture.detectChanges();
    return fixture;
  }

  const textOf = (fixture: { nativeElement: HTMLElement }) =>
    fixture.nativeElement.textContent ?? '';
  const buttons = (root: HTMLElement, label: string) =>
    [...root.querySelectorAll('button')].filter((b) => b.textContent?.trim() === label);

  /** The editor is modal data entry: its content lives in the shared shell's
   * panel (document-level overlay), not inside the host element. Lookups go
   * to the panel; the assertions themselves are unchanged. */
  const panel = (fixture: { nativeElement: HTMLElement }): HTMLElement => {
    const el = document.querySelector<HTMLElement>('.dialog-panel');
    if (!el) throw new Error('no dialog panel rendered');
    return el;
  };
  const panelText = (fixture: { nativeElement: HTMLElement }) =>
    (panel(fixture).textContent ?? '') + textOf(fixture);

  it('edit mode shows the pinned version, rules with citations, and existing overrides as draft state', async () => {
    const fixture = render({
      PlantDetail: plantDetail(),
      PlantCarePlan: plantCarePlan(carePlan()),
    });
    await flush();
    fixture.detectChanges();
    const text = panelText(fixture);
    expect(text).toContain('Edit care plan');
    expect(text).toContain('Monstera');
    expect(text).toContain('Revision 4');
    expect(text).toContain('Every 7 days (from plan start)');
    expect(text).toContain('House Plant Care, Orchard Press, 2023');
    // the stored disable shows as an unchecked rule toggle
    const mistOff = [...panel(fixture).querySelectorAll('input[type="checkbox"]')].filter((c) =>
      (c.closest('div')?.textContent ?? '').includes('mist'),
    );
    expect(mistOff.some((c) => !(c as HTMLInputElement).checked)).toBe(true);
  });

  it('preview shows the next five dates per rule and exactly what saving would cancel', async () => {
    const fixture = render({
      PlantDetail: plantDetail(),
      PlantCarePlan: plantCarePlan(carePlan()),
      PreviewCarePlan: {
        previewCarePlan: {
          preview: {
            occurrences: [
              {
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                dueDate: '2026-11-05',
                reason: null,
              },
              {
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                dueDate: '2026-11-12',
                reason: null,
              },
              {
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                dueDate: '2026-11-19',
                reason: null,
              },
              {
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                dueDate: '2026-11-26',
                reason: null,
              },
              {
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                dueDate: '2026-12-03',
                reason: null,
              },
              {
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                dueDate: '2026-12-10',
                reason: null,
              },
            ],
            wouldCancel: [
              {
                id: 'oc-x',
                planId: 'cp-1',
                plantId: 'pl-1',
                ruleKey: 'water',
                kind: 'ACTION',
                activityKind: 'WATER',
                occurrenceKey: 'k',
                originalDueDate: '2026-11-08',
                currentDueDate: '2026-11-08',
                snoozedUntil: null,
                status: 'DUE',
                completedActivityId: null,
                resolvedAt: null,
                planTimezone: 'UTC',
                reason: null,
              },
            ],
            missingFacts: [],
          },
          problems: [],
        },
      },
    });
    await flush();
    fixture.detectChanges();
    buttons(panel(fixture), 'Preview next dates')[0].click();
    await flush();
    fixture.detectChanges();
    const text = panelText(fixture);
    expect(text).toContain('2026-11-05 · 2026-11-12 · 2026-11-19 · 2026-11-26 · 2026-12-03');
    expect(text).not.toContain('2026-12-10'); // capped at five
    expect(text).toContain('Saving cancels 1 open occurrence(s): water on 2026-11-08');
    const pv = gqlCalls().find((c) => c.op === 'PreviewCarePlan');
    expect(pv!.vars['input']).toMatchObject({
      plantId: 'pl-1',
      timezone: 'UTC',
    });
  });

  it('a timezone change cannot save until previewed and confirmed, then sends confirmRecalculation', async () => {
    const fixture = render({
      PlantDetail: plantDetail(),
      PlantCarePlan: plantCarePlan(carePlan()),
      PreviewCarePlan: {
        previewCarePlan: {
          preview: { occurrences: [], wouldCancel: [], missingFacts: [] },
          problems: [],
        },
      },
      UpdateCarePlan: {
        updateCarePlan: {
          plan: carePlan({ timezone: 'America/Chicago', revision: 5 }),
          problems: [],
        },
      },
    });
    await flush();
    fixture.detectChanges();

    const tz = panel(fixture).querySelector('#pe-tz') as HTMLInputElement;
    tz.value = 'America/Chicago';
    tz.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    expect(panelText(fixture)).toContain('re-dates every future occurrence');
    const save = buttons(panel(fixture), 'Save plan')[0] as HTMLButtonElement;
    expect(save.disabled).toBe(true); // not yet previewed, not confirmed

    buttons(panel(fixture), 'Preview next dates')[0].click();
    await flush();
    fixture.detectChanges();
    const boxes = [
      ...panel(fixture).querySelectorAll('input[type="checkbox"]'),
    ] as HTMLInputElement[];
    const recalc = boxes.find((c) =>
      (c.closest('label')?.textContent ?? '').includes('recalculated'),
    );
    expect(recalc).toBeTruthy();
    expect((buttons(panel(fixture), 'Save plan')[0] as HTMLButtonElement).disabled).toBe(true); // confirmed? no
    recalc!.click();
    fixture.detectChanges();
    expect((buttons(panel(fixture), 'Save plan')[0] as HTMLButtonElement).disabled).toBe(false);

    buttons(panel(fixture), 'Save plan')[0].click();
    await flush();
    const upd = gqlCalls().find((c) => c.op === 'UpdateCarePlan');
    expect(upd!.vars['input']).toMatchObject({
      id: 'cp-1',
      expectedRevision: 4,
      timezone: 'America/Chicago',
      confirmRecalculation: true,
    });
  });

  it('a stale save names the current revision and reloads the plan under the kept draft', async () => {
    let planReads = 0;
    const fixture = render({
      PlantDetail: () => plantDetail(),
      PlantCarePlan: () => {
        planReads += 1;
        return plantCarePlan(carePlan({ revision: planReads > 1 ? 7 : 4 }));
      },
      UpdateCarePlan: {
        updateCarePlan: {
          plan: null,
          problems: [
            {
              code: 'CONFLICT',
              field: null,
              message: 'stale write',
              currentRevision: 7,
            },
          ],
        },
      },
    });
    await flush();
    fixture.detectChanges();
    buttons(panel(fixture), 'Save plan')[0].click();
    await flush();
    fixture.detectChanges();
    expect(panelText(fixture)).toContain('revision 7');
    expect(planReads).toBe(2); // plan reloaded so the next save carries the fresh revision
    expect(panelText(fixture)).toContain('Revision 7');
  });

  it('an older pinned version gets an upgrade review with a rule-by-rule diff', async () => {
    const current = profileVersion(
      3,
      [
        rule('water', {
          recurrence: {
            __typename: 'EveryNDaysRecurrence',
            days: 5,
            anchor: 'PLAN_START',
          },
        }),
        rule('feed', { activityKind: 'FERTILIZE' }),
      ],
      'pv-3',
    );
    const fixture = render({
      PlantDetail: plantDetail(),
      PlantCarePlan: plantCarePlan(carePlan()),
      CareProfileDetail: {
        careProfile: {
          id: 'pr-1',
          category: 'HOUSEPLANT',
          species: 'Monstera deliciosa',
          cultivar: null,
          status: 'REVIEWED',
          revision: 3,
          createdAt: '2026-01-01T00:00:00Z',
          updatedAt: '2026-02-01T00:00:00Z',
          versions: [],
          currentVersion: current,
        },
      },
      UpdateCarePlan: {
        updateCarePlan: {
          plan: carePlan({ profileVersion: current, revision: 5 }),
          problems: [],
        },
      },
    });
    await flush();
    fixture.detectChanges();
    const text = panelText(fixture);
    expect(text).toContain('Profile upgrade available');
    expect(text).toContain('v2 → v3');
    expect(text).toContain('Adds feed');
    expect(text).toContain(
      'water: Every 7 days (from plan start) → Every 5 days (from plan start)',
    );

    buttons(panel(fixture), 'Apply v3')[0].click();
    fixture.detectChanges();
    buttons(panel(fixture), 'Save plan')[0].click();
    await flush();
    const upd = gqlCalls().find((c) => c.op === 'UpdateCarePlan');
    expect(upd!.vars['input']).toMatchObject({
      profileVersionId: 'pv-3',
      expectedRevision: 4,
    });
  });

  it('create mode offers a profile picker and creates with the pinned version', async () => {
    const fixture = render({
      PlantDetail: plantDetail(),
      PlantCarePlan: plantCarePlan(null),
      CareProfilePicker: {
        careProfiles: {
          items: [
            {
              id: 'pr-1',
              category: 'HOUSEPLANT',
              species: 'Monstera deliciosa',
              cultivar: null,
              currentVersion: { id: 'pv-2', version: 2 },
            },
          ],
          pageInfo: { endCursor: null, hasNextPage: false },
        },
      },
      CreateCarePlan: {
        createCarePlan: { plan: carePlan({ id: 'cp-new' }), problems: [] },
      },
      CareProfileDetail: {
        careProfile: {
          id: 'pr-1',
          category: 'HOUSEPLANT',
          species: 'Monstera deliciosa',
          cultivar: null,
          status: 'REVIEWED',
          revision: 1,
          createdAt: '2026-01-01T00:00:00Z',
          updatedAt: '2026-01-01T00:00:00Z',
          versions: [],
          currentVersion: profileVersion(2, [rule('water')], 'pv-2'),
        },
      },
    });
    await flush();
    fixture.detectChanges();
    expect(panelText(fixture)).toContain('New care plan');
    const select = panel(fixture).querySelector('#pe-profile') as HTMLSelectElement;
    expect(select).toBeTruthy();
    expect(select.textContent).toContain('Monstera deliciosa · v2');
    select.value = 'pv-2';
    select.dispatchEvent(new Event('change'));
    await flush(); // the picked version's full body loads on pick
    fixture.detectChanges();
    expect(panelText(fixture)).toContain('Every 7 days (from plan start)'); // pinned rules become editable

    buttons(panel(fixture), 'Create plan')[0].click();
    await flush();
    const create = gqlCalls().find((c) => c.op === 'CreateCarePlan');
    expect(create!.vars['input']).toMatchObject({
      plantId: 'pl-1',
      profileVersionId: 'pv-2',
      timezone: 'UTC',
    });
  });

  it('a missing plant is stated, not faked', async () => {
    const fixture = render({
      PlantDetail: { plant: null },
      PlantCarePlan: { plant: null },
    });
    await flush();
    fixture.detectChanges();
    expect(panelText(fixture)).toContain('Plant not found');
  });
});
