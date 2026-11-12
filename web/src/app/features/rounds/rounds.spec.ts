import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PlantsDocument, RecordActivityDocument } from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient } from '../../core/graphql/client';
import { UiTheme } from '../../ui/theme';
import { Uploader } from '../capture/upload';
import { RoundActions } from './rounds-actions';
import { RoundPage } from './rounds';
@Component({ template: '<h1>Sign in</h1>' })
class LoginStub {}
const conn = (items: unknown[]) => ({ items, pageInfo: { hasNextPage: false, endCursor: null } });
const plants = ['lemon', 'lime'].map((id) => ({
  id,
  name: id === 'lemon' ? 'Meyer Lemon' : 'Lime',
  status: 'ACTIVE',
  coverPhoto: null,
  currentContext: null,
  cultivar: null,
  scientificName: null,
}));
const file = () => new File(['tree'], 'plant.jpg', { type: 'image/jpeg' });

describe('Garden rounds routes', () => {
  let harness: RouterTestingHarness, historyFailure: boolean;
  let session: {
    state: ReturnType<typeof signal<string>>;
    info: ReturnType<typeof signal<{ workspaceId: string }>>;
    ended: ReturnType<typeof signal<string | null>>;
  };
  let gql: { query: ReturnType<typeof vi.fn>; mutate: ReturnType<typeof vi.fn> };
  let uploader: {
    reserve: ReturnType<typeof vi.fn>;
    put: ReturnType<typeof vi.fn>;
    finalize: ReturnType<typeof vi.fn>;
    waitTerminal: ReturnType<typeof vi.fn>;
  };
  beforeEach(() => {
    historyFailure = false;
    session = {
      state: signal('authed'),
      info: signal({ workspaceId: 'fixture' }),
      ended: signal(null),
    };
    gql = {
      query: vi.fn(async (doc: unknown) =>
        doc !== PlantsDocument && historyFailure
          ? { kind: 'network' }
          : {
              kind: 'ok',
              errors: [],
              data: doc === PlantsDocument ? { plants: conn(plants) } : { journal: conn([]) },
            },
      ),
      mutate: vi.fn(async (doc: unknown) => ({
        kind: 'ok',
        errors: [],
        data:
          doc === RecordActivityDocument
            ? { recordActivity: { activity: { id: 'water' }, problems: [] } }
            : { attachPhoto: { photo: { id: 'photo' }, problems: [] } },
      })),
    };
    uploader = {
      reserve: vi.fn(async () => ({ ok: true, asset: { id: 'asset', state: 'RESERVED' } })),
      put: vi.fn(async () => ({ ok: true, sha256: 'a'.repeat(64) })),
      finalize: vi.fn(async () => ({ ok: true, asset: { id: 'asset', state: 'READY' } })),
      waitTerminal: vi.fn(),
    };
    TestBed.configureTestingModule({
      providers: [
        provideRouter([
          { path: 'rounds/:plantId', component: RoundPage },
          { path: 'rounds', component: RoundPage },
          { path: 'login', component: LoginStub },
        ]),
        { provide: GraphQLClient, useValue: gql },
        { provide: Uploader, useValue: uploader },
        { provide: UiTheme, useValue: {} },
        { provide: SessionService, useValue: session },
      ],
    });
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {});
  });
  afterEach(() => {
    TestBed.resetTestingModule();
    vi.restoreAllMocks();
  });
  function root() {
    return harness.routeNativeElement!;
  }
  function button(name: string) {
    return [...root().querySelectorAll<HTMLButtonElement>('button')].find(
      (b) => (b.getAttribute('aria-label') ?? b.textContent?.trim()) === name,
    )!;
  }
  async function settle() {
    for (let i = 0; i < 25; i++) await Promise.resolve();
    harness.detectChanges();
    await harness.fixture.whenStable();
    for (let i = 0; i < 25; i++) await Promise.resolve();
    harness.detectChanges();
  }
  async function render(path = '/rounds/lemon') {
    harness = await RouterTestingHarness.create(path);
    await settle();
  }
  function choose(image: File | null) {
    const input = root().querySelector<HTMLInputElement>('input[type=file]')!;
    Object.defineProperty(input, 'files', { value: image ? [image] : [], configurable: true });
    input.dispatchEvent(new Event('change', { bubbles: true }));
    return input;
  }
  it('opens with only a full plant grid and navigates to a separate plant view', async () => {
    await render('/rounds');
    expect(root().querySelector('.plant-grid')).toBeTruthy();
    expect(root().querySelector('.round-card')).toBeNull();
    expect(button('Take photo')).toBeUndefined();
    root().querySelector<HTMLAnchorElement>('[data-plant-id="lime"]')!.click();
    await settle();
    expect(TestBed.inject(Router).url).toBe('/rounds/lime');
    expect(root().querySelector('.plant-grid')).toBeNull();
    expect(root().querySelector('h1')?.textContent).toBe('Lime');
    expect(button('Back to rounds')).toBeTruthy();
    expect(gql.mutate).not.toHaveBeenCalled();
  });
  it('supports direct plant links and gives them a Back route to the grid', async () => {
    await render('/rounds/lime');
    expect(root().querySelector('.plant-grid')).toBeNull();
    button('Back to rounds').click();
    await settle();
    expect(TestBed.inject(Router).url).toBe('/rounds');
    expect(root().querySelector('.plant-grid')).toBeTruthy();
    expect(root().querySelector('.round-card')).toBeNull();
  });
  it('saves without automatically leaving the plant screen', async () => {
    await render();
    button('Mark watered').click();
    await settle();
    expect(button('Watered today').disabled).toBe(true);
    expect(root().querySelector('h1')?.textContent).toBe('Meyer Lemon');
    expect(TestBed.inject(Router).url).toBe('/rounds/lemon');
  });
  it('keeps the camera target fixed when the route parameter changes', async () => {
    await render();
    const input = root().querySelector<HTMLInputElement>('input[type=file]')!;
    vi.spyOn(input, 'click').mockImplementation(() => {});
    expect(input.getAttribute('capture')).toBe('environment');
    button('Take photo').click();
    await harness.navigateByUrl('/rounds/lime', RoundPage);
    choose(file());
    await settle();
    expect(gql.mutate.mock.calls[0][1].input.plantId).toBe('lemon');
    expect(root().querySelector('h1')?.textContent).toBe('Lime');
  });
  it('preserves a failed photo when the native camera is cancelled', async () => {
    await render();
    uploader.reserve.mockResolvedValueOnce({ ok: false, code: 'NETWORK', message: 'Unconfirmed' });
    const actions = TestBed.inject(RoundActions);
    await actions.takePhoto('lemon', file());
    harness.detectChanges();
    const id = actions.photos()[0].id;
    vi.spyOn(
      root().querySelector<HTMLInputElement>('input[type=file]')!,
      'click',
    ).mockImplementation(() => {});
    button('Retake photo').click();
    choose(null);
    await settle();
    expect(actions.photos()[0].id).toBe(id);
    expect(uploader.reserve).toHaveBeenCalledTimes(1);
  });
  it('pauses watering while today history is unavailable', async () => {
    historyFailure = true;
    await render();
    expect(button('Mark watered').disabled).toBe(true);
    expect(button('Take photo').disabled).toBe(false);
    historyFailure = false;
    button('Retry history').click();
    await settle();
    expect(button('Mark watered').disabled).toBe(false);
    expect(gql.mutate).not.toHaveBeenCalled();
  });
  it('continues saving a photo after returning to the grid', async () => {
    let resume!: (value: unknown) => void;
    uploader.put.mockReturnValueOnce(new Promise((r) => (resume = r)));
    await render();
    vi.spyOn(
      root().querySelector<HTMLInputElement>('input[type=file]')!,
      'click',
    ).mockImplementation(() => {});
    button('Take photo').click();
    choose(file());
    await settle();
    const actions = TestBed.inject(RoundActions);
    expect(actions.photos()[0].state).toBe('saving');
    await harness.navigateByUrl('/rounds', RoundPage);
    resume({ ok: true, sha256: 'a'.repeat(64) });
    await settle();
    expect(actions.photos()[0].state).toBe('saved');
    expect(root().querySelector('.round-card')).toBeNull();
    expect(gql.mutate.mock.calls[0][1].input.plantId).toBe('lemon');
  });
  it('does not substitute another plant for an unknown direct link', async () => {
    await render('/rounds/missing');
    expect(root().querySelector('h1')?.textContent).toBe('Plant unavailable');
    expect(button('Take photo')).toBeUndefined();
    expect(root().querySelector('.plant-grid')).toBeNull();
  });
  it('redirects an expired full-screen session to login with its return URL', async () => {
    await render();
    session.ended.set('expired');
    session.state.set('anon');
    await settle();
    expect(TestBed.inject(Router).url).toContain('/login?returnUrl=%2Frounds%2Flemon');
  });
  it('clears Saving photo on the same screen after a delayed successful save', async () => {
    let resume!: (value: unknown) => void;
    uploader.put.mockReturnValueOnce(new Promise((resolve) => (resume = resolve)));
    await render();
    vi.spyOn(
      root().querySelector<HTMLInputElement>('input[type=file]')!,
      'click',
    ).mockImplementation(() => {});
    button('Take photo').click();
    choose(file());
    await settle();
    expect(button('Saving photo…')?.disabled).toBe(true);
    resume({ ok: true, sha256: 'a'.repeat(64) });
    await settle();
    expect(TestBed.inject(RoundActions).photos()[0].state).toBe('saved');
    expect(button('Take photo')?.disabled).toBe(false);
    expect(button('Saving photo…')).toBeUndefined();
    expect(TestBed.inject(Router).url).toBe('/rounds/lemon');
  });
});
