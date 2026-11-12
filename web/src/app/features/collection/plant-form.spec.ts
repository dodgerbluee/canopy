import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, Router } from '@angular/router';
import { of } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { PlantForm } from './plant-form';

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
      calls.push({
        op,
        vars: (body.variables ?? {}) as Record<string, unknown>,
      });
      const h = handlers[op];
      if (h === 'network') throw new TypeError('fetch failed');
      return new Response(JSON.stringify({ data: h }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

const flush = () => new Promise((r) => setTimeout(r, 0));

function detailFixture(over: Record<string, unknown> = {}) {
  return {
    plant: {
      id: 'pid-1',
      name: 'Meyer lemon',
      scientificName: null,
      cultivar: 'Meyer',
      rootstock: null,
      type: 'INDIVIDUAL',
      count: null,
      category: 'CITRUS',
      acquiredOn: null,
      plantedOn: null,
      estimatedAgeYears: null,
      status: 'ACTIVE',
      revision: 2,
      createdAt: '2026-11-01T00:00:00Z',
      updatedAt: '2026-11-01T00:00:00Z',
      photoCount: 0,
      tags: [],
      coverPhoto: null,
      currentContext: null,
      acquisition: null,
      ...over,
    },
  };
}

describe('PlantForm', () => {
  let session: SessionService;
  let navigate: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    navigate = vi.fn().mockResolvedValue(true);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    // the entry dialog renders in a CDK overlay on document.body
    document.body.innerHTML = '';
  });

  function render(mode: 'create' | 'edit') {
    const paramMap =
      mode === 'edit'
        ? of({ get: (k: string) => (k === 'id' ? 'pid-1' : null) })
        : of({ get: () => null });
    // the component reads the id off snapshot.paramMap, so the mock snapshot
    // must carry the same id the observable paramMap exposes; edit routes hang
    // the id on the parent route, hence parent: null here.
    const snapshotParamMap =
      mode === 'edit'
        ? { get: (k: string) => (k === 'id' ? 'pid-1' : null) }
        : new Map<string, string>();
    TestBed.configureTestingModule({
      providers: [
        { provide: Router, useValue: { navigate } },
        {
          provide: ActivatedRoute,
          useValue: {
            paramMap,
            snapshot: { paramMap: snapshotParamMap },
            parent: null,
          },
        },
      ],
    });
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({
      workspaceId: 'w1',
      expiresAt: '2026-12-01T00:00:00Z',
      csrfToken: 'c',
    });
    const fixture = TestBed.createComponent(PlantForm);
    fixture.detectChanges();
    return fixture;
  }

  /** The form now renders inside the shared modal shell, so its content lives
   * in the overlay panel (document) rather than under the host element. The
   * payload assertions below are unchanged — only the lookup root moved. */
  function panel(): HTMLElement {
    const found = document.querySelector<HTMLElement>('.dialog-panel');
    if (!found) throw new Error('no entry-dialog panel rendered');
    return found;
  }

  function setInput(root: HTMLElement, id: string, value: string): void {
    const el = root.querySelector(`#${id}`) as HTMLInputElement;
    el.value = value;
    el.dispatchEvent(new Event('input', { bubbles: true }));
  }

  it('create: blanks stay unknown — cultivar/count travel as null', async () => {
    const calls = stubFetch({
      Locations: { locations: [] },
      Tags: {
        tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } },
      },
      CreatePlant: {
        createPlant: {
          clientMutationId: null,
          plant: { id: 'new-1', revision: 1 },
          problems: [],
        },
      },
    });
    const fixture = render('create');
    await flush();
    fixture.detectChanges();
    const root = panel();
    setInput(root, 'f-name', 'Alpine strawberries');
    setInput(root, 'f-count', ''); // unknown
    (root.querySelector('form') as HTMLElement).dispatchEvent(new Event('submit'));
    await flush();
    await flush();

    const create = calls.find((c) => c.op === 'CreatePlant');
    expect(create).toBeTruthy();
    const input = create!.vars['input'] as Record<string, unknown>;
    expect(input['name']).toBe('Alpine strawberries');
    expect(input['cultivar']).toBeNull();
    expect(input['count']).toBeNull();
    expect(input['initialContext']).toBeUndefined(); // no context fields touched
    expect(input['acquisition']).toBeUndefined();
    expect(String(create!.vars['clientMutationId'])).toMatch(/:create$/);
    expect(navigate).toHaveBeenCalledWith(['/plants', 'new-1']);
  });

  it('create: invalid count blocks the submit with a field error', async () => {
    const calls = stubFetch({
      Locations: { locations: [] },
      Tags: {
        tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } },
      },
      CreatePlant: {
        createPlant: {
          clientMutationId: null,
          plant: { id: 'x', revision: 1 },
          problems: [],
        },
      },
    });
    const fixture = render('create');
    await flush();
    fixture.detectChanges();
    const root = panel();
    setInput(root, 'f-name', 'Fig');
    setInput(root, 'f-count', '0');
    root.querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    fixture.detectChanges();
    expect(root.textContent).toContain('positive whole number');
    expect(calls.some((c) => c.op === 'CreatePlant')).toBe(false);
  });

  it('create: server VALIDATION on input.name lands on the name field', async () => {
    stubFetch({
      Locations: { locations: [] },
      Tags: {
        tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } },
      },
      CreatePlant: {
        createPlant: {
          clientMutationId: null,
          plant: null,
          problems: [
            {
              code: 'VALIDATION',
              field: 'input.name',
              message: 'A plant with that name already exists.',
              currentRevision: null,
            },
          ],
        },
      },
    });
    const fixture = render('create');
    await flush();
    fixture.detectChanges();
    const root = panel();
    setInput(root, 'f-name', 'Meyer lemon');
    root.querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    await flush();
    fixture.detectChanges();
    expect(root.querySelector('#e-name')?.textContent).toContain('already exists');
  });

  it('edit: prefills from PlantDetail and sends the loaded revision', async () => {
    const calls = stubFetch({
      Locations: { locations: [] },
      Tags: {
        tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } },
      },
      PlantDetail: detailFixture(),
      UpdatePlant: {
        updatePlant: {
          clientMutationId: null,
          plant: { id: 'pid-1', revision: 3 },
          problems: [],
        },
      },
    });
    const fixture = render('edit');
    await flush();
    await flush();
    fixture.detectChanges();
    const root = panel();
    expect((root.querySelector('#f-name') as HTMLInputElement).value).toBe('Meyer lemon');

    setInput(root, 'f-cult', 'Meyer Improved');
    root.querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    await flush();
    const upd = calls.find((c) => c.op === 'UpdatePlant');
    expect(upd!.vars['input']).toMatchObject({
      id: 'pid-1',
      expectedRevision: 2,
      cultivar: 'Meyer Improved',
    });
  });

  it('edit: revision conflict explains the current revision and offers reload', async () => {
    stubFetch({
      Locations: { locations: [] },
      Tags: {
        tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } },
      },
      PlantDetail: detailFixture(),
      UpdatePlant: {
        updatePlant: {
          clientMutationId: null,
          plant: null,
          problems: [
            {
              code: 'CONFLICT',
              field: 'input.expectedRevision',
              message: 'Stale revision.',
              currentRevision: 5,
            },
          ],
        },
      },
    });
    const fixture = render('edit');
    await flush();
    await flush();
    fixture.detectChanges();
    const root = panel();
    setInput(root, 'f-name', 'Renamed lemon');
    root.querySelector('form')!.dispatchEvent(new Event('submit'));
    await flush();
    await flush();
    fixture.detectChanges();
    const alert = root.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain('revision 5');
    expect(alert?.textContent).toContain('Nothing was saved');
    expect(navigate).not.toHaveBeenCalledWith(['/plants', 'pid-1']);
  });

  it('edit: missing plant renders an honest not-found, not a blank form', async () => {
    stubFetch({
      Locations: { locations: [] },
      Tags: {
        tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } },
      },
      PlantDetail: { plant: null },
    });
    const fixture = render('edit');
    await flush();
    await flush();
    fixture.detectChanges();
    // the not-found state now renders inside the always-open modal shell, so
    // query the overlay panel instead of the host element
    const panelEl = document.querySelector<HTMLElement>('.dialog-panel');
    expect(panelEl?.textContent).toContain('Plant not found');
  });
  it('archives only after an explicit confirmation, with the current revision', async () => {
    const calls = stubFetch({
      PlantDetail: detailFixture(),
      Locations: { locations: [] },
      Tags: { tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } } },
      ArchivePlant: { archivePlant: { plant: { id: 'pid-1', revision: 3 }, problems: [] } },
    });
    const fixture = render('edit');
    await flush();
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    const button = (name: string) =>
      [...panel().querySelectorAll<HTMLButtonElement>('button')].find(
        (b) => b.textContent?.trim() === name,
      )!;
    expect(calls.some((c) => c.op === 'ArchivePlant')).toBe(false);
    button('Archive plant').click();
    fixture.detectChanges();
    await flush();
    expect(calls.some((c) => c.op === 'ArchivePlant')).toBe(false);
    button('Confirm archive').click();
    await flush();
    fixture.detectChanges();
    expect(calls.find((c) => c.op === 'ArchivePlant')?.vars).toMatchObject({
      input: { id: 'pid-1', expectedRevision: 2 },
    });
    expect(navigate).toHaveBeenCalledWith(['/plants', 'pid-1']);
  });
  it('keeps archive conflicts in the edit form with the revision warning', async () => {
    const calls = stubFetch({
      PlantDetail: detailFixture(),
      Locations: { locations: [] },
      Tags: { tags: { items: [], pageInfo: { endCursor: null, hasNextPage: false } } },
      ArchivePlant: {
        archivePlant: {
          plant: null,
          problems: [
            {
              code: 'CONFLICT',
              field: 'input.expectedRevision',
              message: 'Stale revision.',
              currentRevision: 4,
            },
          ],
        },
      },
    });
    const fixture = render('edit');
    await flush();
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    const button = (name: string) =>
      [...panel().querySelectorAll<HTMLButtonElement>('button')].find(
        (b) => b.textContent?.trim() === name,
      )!;
    button('Archive plant').click();
    fixture.detectChanges();
    button('Confirm archive').click();
    await flush();
    fixture.detectChanges();
    expect(panel().textContent).toContain('revision 4');
    expect(navigate).not.toHaveBeenCalled();
    expect(calls.filter((c) => c.op === 'ArchivePlant')).toHaveLength(1);
  });
});
