/**
 * Rounds runtime, phase 2: read-only data helpers for "what did I do today".
 * Pure functions over the typed GraphQLClient and the generated documents —
 * no Angular injection, no global state, no browser storage. Every failure
 * (transport, session, abort, GraphQL error, broken pagination) throws; an
 * empty result always means a genuinely empty day/garden, never a swallowed
 * failure.
 */
import {
  JournalDocument,
  PlantsDocument,
  type JournalQuery,
  type PlantFieldsFragment,
  type PlantsQuery,
} from '@canopy/contracts';
import type { GraphQLClient, GqlResult } from '../../core/graphql/client';
import { localDateInput } from '../journal/journal-view';

/** Page size for both walks; small enough to keep responses cheap, large
 * enough that a typical garden or day resolves in one request. */
const PAGE_SIZE = 100;

type PlantPage = PlantsQuery['plants'];
type PlantRow = PlantPage['items'][number];
type JournalPage = JournalQuery['journal'];
type JournalRow = JournalPage['items'][number];
type ActivityRow = Extract<JournalRow, { __typename: 'ActivityEntry' }>;
type PhotoRow = Extract<JournalRow, { __typename: 'PhotoEntry' }>;

/** What the owner actually did today, keyed by plant. */
export interface RoundHistory {
  /** yyyy-mm-dd in the browser's local calendar, from localDateInput. */
  dayKey: string;
  /** Plants with a real, non-voided WATER activity recorded on this day. */
  watered: Set<string>;
  /** plantId -> photo ids genuinely captured on this day. */
  photos: Map<string, Set<string>>;
}

/**
 * The local calendar day containing `now`, as an inclusive instant range.
 *
 * Boundaries are built from local calendar parts, so a DST day is whatever
 * length it really is (23 or 25 hours) rather than a fixed 24h slice, and the
 * key is never a UTC date cut out of an ISO string.
 */
export function roundDay(now: Date): { key: string; from: string; to: string } {
  if (!(now instanceof Date) || Number.isNaN(now.getTime())) {
    throw new Error('roundDay needs a real date.');
  }
  const y = now.getFullYear();
  const m = now.getMonth();
  const d = now.getDate();
  const start = new Date(y, m, d);
  const end = new Date(new Date(y, m, d + 1).getTime() - 1);
  return {
    key: localDateInput(now.toISOString()),
    from: start.toISOString(),
    to: end.toISOString(),
  };
}

/**
 * Every ACTIVE plant in this workspace, in NAME_ASC order.
 *
 * Pages are followed to completion, so the caller never sees a half-loaded
 * garden: the result is returned only once the last page is confirmed.
 * Duplicate ids across pages collapse to one row.
 */
export async function loadRoundPlants(
  gql: GraphQLClient,
  signal?: AbortSignal,
): Promise<PlantFieldsFragment[]> {
  const byId = new Map<string, PlantRow>();
  const seen = new Set<string>();
  let after: string | null = null;
  for (;;) {
    const conn = readPage(
      await gql.query(
        PlantsDocument,
        { first: PAGE_SIZE, after, order: 'NAME_ASC', filter: { status: ['ACTIVE'] } },
        signal,
      ),
      (data) => data.plants,
      'the plant list',
    );
    for (const row of conn.items) {
      // The filter already asks for ACTIVE; re-check so a server-side default
      // change can never leak an archived plant into the round.
      if (row.status === 'ACTIVE' && !byId.has(row.id)) byId.set(row.id, row);
    }
    if (!conn.pageInfo.hasNextPage) break;
    after = nextCursor(conn, after, 'the plant list', seen);
  }
  return [...byId.values()];
}

/**
 * Waterings and photos for the local day containing `now`, workspace-scoped
 * (no plantId filter). Every journal page in the range is read before the
 * answer is returned, so a partially checked day is never reported as done.
 */
export async function loadRoundHistory(
  gql: GraphQLClient,
  now: Date,
  signal?: AbortSignal,
): Promise<RoundHistory> {
  const day = roundDay(now);
  const watered = new Set<string>();
  const photos = new Map<string, Set<string>>();

  const seen = new Set<string>();
  let after: string | null = null;
  for (;;) {
    const conn = readPage(
      await gql.query(
        JournalDocument,
        {
          first: PAGE_SIZE,
          after,
          filter: {
            entryTypes: ['ACTIVITY', 'PHOTO'],
            includeTombstoned: false,
            from: day.from,
            to: day.to,
          },
        },
        signal,
      ),
      (data) => data.journal,
      'today\u2019s journal',
    );
    for (const item of conn.items) {
      if (item.__typename === 'ActivityEntry') {
        const plantId = wateredPlant(item, day.key);
        if (plantId) watered.add(plantId);
      } else if (item.__typename === 'PhotoEntry') {
        const photo = datedPhoto(item, day.key);
        if (photo) {
          const ids = photos.get(photo.plantId) ?? new Set<string>();
          ids.add(photo.id);
          photos.set(photo.plantId, ids);
        }
      }
      // Every other journal union member is not something the owner did with
      // their hands today; ignore it rather than infer a task from it.
    }
    if (!conn.pageInfo.hasNextPage) break;
    after = nextCursor(conn, after, 'today\u2019s journal', seen);
  }
  return { dayKey: day.key, watered, photos };
}

/** A watering counts only when the activity itself is a real, un-voided WATER
 * whose actual occurredAt falls on this local day. */
function wateredPlant(entry: ActivityRow, dayKey: string): string | null {
  if (entry.tombstonedAt !== null) return null;
  const activity = entry.activity;
  if (activity.kind !== 'WATER' || activity.voidedAt !== null) return null;
  return localDateInput(activity.occurredAt) === dayKey ? activity.plantId : null;
}

/** A photo counts only when it has a stated capture instant that is not the
 * upload fallback, is not deleted, and was captured on this local day. The
 * upload date is never treated as the true capture date. */
function datedPhoto(entry: PhotoRow, dayKey: string): PhotoRow['photo'] | null {
  if (entry.tombstonedAt !== null) return null;
  const photo = entry.photo;
  if (photo.takenAt === null || photo.dateSource === 'UPLOAD_FALLBACK') return null;
  if (photo.deletedAt !== null) return null;
  return localDateInput(photo.takenAt) === dayKey ? photo : null;
}

/** One page of a connection, whatever query it came from. */
interface Connection {
  items: unknown[];
  pageInfo: { hasNextPage: boolean; endCursor: string | null };
}

/** Keep generated query result types intact when selecting their connection. */
function readPage<T, C extends Connection>(
  res: GqlResult<T>,
  pick: (data: T) => C,
  what: string,
): C {
  if (res.kind !== 'ok') throw new Error(`${what} could not be loaded: ${failureNote(res.kind)}`);
  if (res.errors.length) throw new Error(`${what} could not be loaded: ${res.errors[0].message}`);
  if (res.data == null) throw new Error(`${what} returned no data.`);
  const conn = pick(res.data);
  if (!conn || !Array.isArray(conn.items) || !conn.pageInfo)
    throw new Error(`${what} came back in an unexpected shape.`);
  return conn;
}

/** Pagination guard: a page that claims more but cannot say how to get there
 * (or repeats the cursor it was given) would silently drop later rows. */
function nextCursor(
  conn: { pageInfo: { hasNextPage: boolean; endCursor: string | null } },
  previous: string | null,
  what: string,
  seen: Set<string>,
): string {
  const cursor = conn.pageInfo.endCursor;
  if (cursor === null || cursor === '' || cursor === previous || seen.has(cursor)) {
    throw new Error(`${what} reports more results but gave no usable cursor.`);
  }
  seen.add(cursor);
  return cursor;
}

function failureNote(kind: 'auth' | 'network' | 'aborted'): string {
  switch (kind) {
    case 'auth':
      return 'your session is no longer valid.';
    case 'network':
      return 'the server could not be reached.';
    case 'aborted':
      return 'the request was cancelled.';
    default: {
      const _never: never = kind;
      return _never;
    }
  }
}
