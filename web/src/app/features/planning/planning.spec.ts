import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GraphQLClient } from '../../core/graphql/client';
import { PlanningPage } from './planning';

const context = {
  session: {
    workspace: {
      id: 'workspace',
      displayName: 'Garden',
      timezone: 'America/Chicago',
      defaultCurrency: 'USD',
      settingsRevision: 1,
      location: null,
    },
  },
};
const empty = {
  timezone: 'America/Chicago',
  today: '2026-11-15',
  items: [],
  pageInfo: { hasNextPage: false, endCursor: null },
};
const ok = (data: unknown) => ({ kind: 'ok', data, errors: [] });
describe('planning page reads', () => {
  let gql: { query: ReturnType<typeof vi.fn>; mutate: ReturnType<typeof vi.fn> };
  beforeEach(() => {
    gql = {
      query: vi.fn(async (doc) => {
        const name = doc.definitions.find((v: { kind: string }) => v.kind === 'OperationDefinition')
          ?.name?.value;
        return ok(
          name === 'Session'
            ? context
            : name === 'Locations'
              ? { locations: [] }
              : { plantingBoard: empty },
        );
      }),
      mutate: vi.fn(),
    };
    TestBed.configureTestingModule({
      providers: [provideRouter([]), { provide: GraphQLClient, useValue: gql }],
    });
  });
  afterEach(() => TestBed.resetTestingModule());
  async function render() {
    const f = TestBed.createComponent(PlanningPage);
    f.detectChanges();
    await f.whenStable();
    await vi.waitFor(() => expect(f.componentInstance.loading()).toBe(false));
    f.detectChanges();
    return f;
  }
  it('uses a single board query and never writes when loading', async () => {
    const f = await render();
    expect(f.componentInstance.board()?.items).toEqual([]);
    expect(gql.mutate).not.toHaveBeenCalled();
    expect(
      gql.query.mock.calls.filter((call) =>
        call[0].definitions.some(
          (v: { kind: string; name?: { value: string } }) => v.name?.value === 'PlantingBoard',
        ),
      ),
    ).toHaveLength(1);
  });
  it('shows failure rather than treating an unreachable garden as an empty list', async () => {
    gql.query.mockResolvedValue({ kind: 'network' });
    const f = await render();
    expect(f.componentInstance.board()).toBeNull();
    expect(f.componentInstance.error()).toContain('reach');
    expect((f.nativeElement as HTMLElement).textContent).not.toContain('Plan your first planting');
  });
  it('rejects a late previous-horizon response', async () => {
    const f = await render();
    let resolve!: (value: unknown) => void;
    const older = new Promise((r) => (resolve = r));
    gql.query
      .mockReturnValueOnce(older)
      .mockResolvedValueOnce(ok({ plantingBoard: { ...empty, today: '2026-12-01' } }));
    const pending = f.componentInstance.load();
    await f.componentInstance.load();
    resolve(ok({ plantingBoard: { ...empty, today: '2026-10-01' } }));
    await pending;
    expect(f.componentInstance.board()?.today).toBe('2026-12-01');
  });
});
