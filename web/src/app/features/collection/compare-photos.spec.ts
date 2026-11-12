import { TestBed } from '@angular/core/testing';
import { ActivatedRoute, Params, Router } from '@angular/router';
import { BehaviorSubject } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { ComparePhotos } from './compare-photos';

/** Minimal PlantPhoto row (PhotoFields) as the server would return it. */
function photoRow(id: string, over: Record<string, unknown> = {}) {
  return {
    id,
    plantId: 'p1',
    takenAt: '2026-05-01T10:00:00Z',
    dateSource: 'USER',
    timezone: 'UTC',
    caption: null,
    viewpoint: 'WHOLE',
    isCover: false,
    revision: 1,
    deletedAt: null,
    createdAt: '2026-05-01T12:00:00Z',
    updatedAt: '2026-05-01T12:00:00Z',
    asset: {
      id: `a-${id}`,
      purpose: 'PHOTO',
      state: 'READY',
      byteCount: 100,
      mimeType: 'image/jpeg',
      sha256: 'x',
      displayName: `${id}.jpg`,
      errorCode: null,
      expiresAt: null,
      variants: [{ name: 'thumb', widthPx: 320, heightPx: 240 }],
      createdAt: '2026-05-01T12:00:00Z',
      updatedAt: '2026-05-01T12:00:00Z',
    },
    ...over,
  };
}

function journalEnvelope(photos: unknown[]) {
  return {
    journal: {
      pageInfo: { hasNextPage: false, endCursor: null },
      items: photos.map((p) => {
        const ph = p as ReturnType<typeof photoRow>;
        return {
          __typename: 'PhotoEntry',
          id: `je-${ph.id}`,
          plantId: 'p1',
          occurredAt: ph.createdAt,
          createdAt: ph.createdAt,
          summary: '',
          tombstonedAt: null,
          photo: ph,
        };
      }),
    },
  };
}

function stubFetch(series: unknown[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as { query: string };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      if (op !== 'Journal') throw new Error(`unexpected operation ${op}`);
      return new Response(JSON.stringify({ data: journalEnvelope(series) }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe('ComparePhotos', () => {
  let current: Params;
  let params$: BehaviorSubject<{ get(k: string): string | null }>;

  const toMap = (p: Params) => ({ get: (k: string) => p[k] ?? null });

  beforeEach(() => {
    current = {};
    params$ = new BehaviorSubject(toMap(current));
    TestBed.configureTestingModule({
      providers: [
        {
          provide: Router,
          useValue: {
            navigate: vi.fn(async (_cmds: unknown, extras?: { queryParams?: Params }) => {
              for (const [k, v] of Object.entries(extras?.queryParams ?? {})) {
                if (v === null) delete current[k];
                else current[k] = String(v);
              }
              params$.next(toMap(current));
              return true;
            }),
          },
        },
        {
          provide: ActivatedRoute,
          useValue: {
            snapshot: { paramMap: { get: (k: string) => (k === 'id' ? 'p1' : null) } },
            queryParamMap: params$,
          },
        },
      ],
    });
    const session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({ workspaceId: 'w1', expiresAt: '2026-12-01T00:00:00Z', csrfToken: 'c' });
  });

  afterEach(() => vi.unstubAllGlobals());

  async function render() {
    const fixture = TestBed.createComponent(ComparePhotos);
    fixture.detectChanges();
    await flush();
    fixture.detectChanges();
    return fixture;
  }

  const two = () => [
    photoRow('ph1', { takenAt: '2026-05-01T10:00:00Z' }),
    photoRow('ph2', {
      takenAt: null,
      dateSource: 'UPLOAD_FALLBACK',
      createdAt: '2026-08-09T18:30:00Z',
      asset: { id: 'a-ph2' },
    }),
  ];

  const text = (f: { nativeElement: HTMLElement }) => f.nativeElement.textContent ?? '';
  const btn = (f: { nativeElement: HTMLElement }, name: string) =>
    Array.from(f.nativeElement.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === name,
    )!;

  it('deep link renders the pair with honest date captions and provenance', async () => {
    stubFetch(two());
    current = { a: 'ph1', b: 'ph2' };
    params$.next(toMap(current));
    const f = await render();
    expect(text(f)).toContain('May 1');
    expect(text(f)).toContain('Aug 9');
    expect(text(f)).toContain('Owner-stated date');
    expect(text(f)).toContain('Upload date — not capture date');
    const alts = Array.from((f.nativeElement as HTMLElement).querySelectorAll('img')).map((i) =>
      i.getAttribute('alt'),
    );
    expect(alts).toEqual(['Photo from May 1', 'Photo from Aug 9']);
  });

  it('no growth percentage is ever rendered', async () => {
    stubFetch(two());
    current = { a: 'ph1', b: 'ph2' };
    params$.next(toMap(current));
    const f = await render();
    expect(text(f)).not.toMatch(/\d+(\.\d+)?\s*%/);
  });

  it('mismatched viewpoints are labelled; matched ones say so', async () => {
    stubFetch([photoRow('ph1'), photoRow('ph2', { viewpoint: 'LEAF', asset: { id: 'a-ph2' } })]);
    current = { a: 'ph1', b: 'ph2' };
    params$.next(toMap(current));
    const f = await render();
    expect(text(f)).toContain('Viewpoints differ');
    expect(text(f)).toContain('whole plant');
    expect(text(f)).toContain('leaf');
  });

  it('matched viewpoints render the same-viewpoint note, not the mismatch one', async () => {
    stubFetch(two());
    current = { a: 'ph1', b: 'ph2' };
    params$.next(toMap(current));
    const f = await render();
    expect(text(f)).toContain('Same viewpoint');
    expect(text(f)).not.toContain('Viewpoints differ');
  });

  it('a deleted selection recovers to the picker with an honest note', async () => {
    stubFetch(two());
    current = { a: 'ph1', b: 'gone' };
    params$.next(toMap(current));
    const f = await render();
    expect(text(f)).toContain('no longer available');
    expect(text(f)).toContain('Use as left');
    expect(text(f)).toContain('Choose a left and a right photo to compare.');
  });

  it('picker: two picks build the pair; change selection returns to the picker', async () => {
    stubFetch(two());
    const f = await render();
    expect(text(f)).toContain('Pick two photos');
    const rows = () => (f.nativeElement as HTMLElement).querySelectorAll('li');
    rows()[0].querySelector('button')!.click(); // ph1 → left
    await flush();
    f.detectChanges();
    const rightBtns = Array.from(
      (f.nativeElement as HTMLElement).querySelectorAll('button'),
    ).filter((b) => b.textContent?.includes('Use as right'));
    rightBtns[1].click(); // ph2 → right
    await flush();
    f.detectChanges();
    expect(text(f)).toContain('May 1');
    expect(text(f)).toContain('Aug 9');
    btn(f, 'Change selection').click();
    await flush();
    f.detectChanges();
    expect(text(f)).toContain('Pick two photos');
  });

  it('swap exchanges left and right', async () => {
    stubFetch(two());
    current = { a: 'ph1', b: 'ph2' };
    params$.next(toMap(current));
    const f = await render();
    btn(f, 'Swap left/right').click();
    await flush();
    f.detectChanges();
    const alts = Array.from((f.nativeElement as HTMLElement).querySelectorAll('img')).map((i) =>
      i.getAttribute('alt'),
    );
    expect(alts).toEqual(['Photo from Aug 9', 'Photo from May 1']);
  });

  it('overlay slider is keyboard-driven: range input moves the wipe', async () => {
    stubFetch(two());
    current = { a: 'ph1', b: 'ph2' };
    params$.next(toMap(current));
    const f = await render();
    btn(f, 'Overlay slider').click();
    f.detectChanges();
    const range = (f.nativeElement as HTMLElement).querySelector(
      'input[type=range]',
    ) as HTMLInputElement;
    expect(range.getAttribute('aria-label')).toBe('Reveal right photo percentage');
    expect(text(f)).toContain('Arrow keys move the handle');
    range.value = '70';
    range.dispatchEvent(new Event('input'));
    f.detectChanges();
    const imgs = (f.nativeElement as HTMLElement).querySelectorAll('img');
    expect(imgs[1].getAttribute('style')).toContain('inset(0 0 0 70%)');
  });

  it('a plant with one photo honestly refuses a comparison', async () => {
    stubFetch([photoRow('ph1')]);
    const f = await render();
    expect(text(f)).toContain('This plant has 1 photo.');
  });
});
