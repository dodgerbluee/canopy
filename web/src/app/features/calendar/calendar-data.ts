import {
  CalendarEventsDocument,
  PlantsDocument,
  SessionDocument,
  type CalendarEventsQuery,
  type PlantFieldsFragment,
} from '@canopy/contracts';
import type { GraphQLClient, GqlResult } from '../../core/graphql/client';

export type CalendarItem = CalendarEventsQuery['calendarEvents']['items'][number];
export function resultData<T>(result: GqlResult<T>): T {
  if (result.kind !== 'ok')
    throw new Error(
      result.kind === 'auth'
        ? 'Your session needs to be refreshed.'
        : 'Could not reach the server. Please retry.',
    );
  if (result.errors.length) throw new Error(result.errors[0].message);
  if (!result.data) throw new Error('The server returned no data. Please retry.');
  return result.data;
}
export async function calendarPlants(
  gql: GraphQLClient,
  signal: AbortSignal,
): Promise<PlantFieldsFragment[]> {
  const plants = new Map<string, PlantFieldsFragment>();
  let after: string | null = null;
  const cursors = new Set<string>();
  for (;;) {
    const page = resultData(
      await gql.query(PlantsDocument, { first: 100, after, order: 'NAME_ASC' }, signal),
    ).plants;
    for (const plant of page.items) plants.set(plant.id, plant);
    if (!page.pageInfo.hasNextPage) return [...plants.values()];
    after = nextPage(page.pageInfo.endCursor, cursors);
  }
}
export async function calendarEvents(
  gql: GraphQLClient,
  from: string,
  to: string,
  signal: AbortSignal,
): Promise<CalendarItem[]> {
  const items = new Map<string, CalendarItem>();
  let after: string | null = null;
  const cursors = new Set<string>();
  for (;;) {
    const page = resultData(
      await gql.query(CalendarEventsDocument, { from, to, first: 100, after }, signal),
    ).calendarEvents;
    for (const item of page.items) items.set(item.id, item);
    if (!page.pageInfo.hasNextPage) return [...items.values()];
    after = nextPage(page.pageInfo.endCursor, cursors);
  }
}
function nextPage(cursor: string | null, seen: Set<string>): string {
  if (!cursor || seen.has(cursor))
    throw new Error('The calendar returned an incomplete page. Please retry.');
  seen.add(cursor);
  return cursor;
}
export async function calendarWorkspace(gql: GraphQLClient, signal: AbortSignal) {
  const workspace = resultData(await gql.query(SessionDocument, {}, signal)).session.workspace;
  if (!workspace) throw new Error('Workspace settings could not be loaded.');
  return workspace;
}

/** UTC is used only for civil-date arithmetic; events are bucketed by server-provided workspace day. */
export function civilDate(key: string): Date {
  return new Date(`${key}T12:00:00Z`);
}
export function dayKey(date: Date): string {
  return date.toISOString().slice(0, 10);
}
export function workspaceDay(now: Date, timezone: string): string {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(now);
  const get = (type: string) => parts.find((p) => p.type === type)!.value;
  return `${get('year')}-${get('month')}-${get('day')}`;
}
export function monthDays(month: string): string[] {
  const first = civilDate(month + '-01');
  const last = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0, 12));
  first.setUTCDate(1 - first.getUTCDay());
  last.setUTCDate(last.getUTCDate() + 6 - last.getUTCDay());
  const days: string[] = [];
  for (let d = first; d <= last; d = new Date(d.getTime() + 86400000)) days.push(dayKey(d));
  return days;
}
export function shiftMonth(month: string, offset: number): string {
  const d = civilDate(month + '-01');
  d.setUTCMonth(d.getUTCMonth() + offset);
  return dayKey(d).slice(0, 7);
}
/** Exact rain-depth conversion. Blank means unknown, never zero. */
export function rainMillimeters(raw: string, unit: 'mm' | 'in'): string | null {
  const value = raw.trim();
  if (!value) return null;
  const precision = unit === 'in' ? 4 : 6;
  if (!new RegExp(`^\\d+(?:\\.\\d{1,${precision}})?$`).test(value) || value.length > 20)
    throw new Error(
      `Enter a positive depth with up to ${precision} decimal places, or leave it blank.`,
    );
  const [whole, fraction = ''] = value.split('.');
  const scale = 10n ** BigInt(fraction.length);
  const scaled = BigInt(whole) * scale + BigInt(fraction || '0');
  const numerator = unit === 'in' ? scaled * 254n : scaled * 10n;
  const denominator = scale * 10n;
  if (numerator > 10000n * denominator) throw new Error('Rain depth cannot exceed 10000 mm.');
  const result = ((numerator * 1000000n) / denominator).toString().padStart(7, '0');
  return (result.slice(0, -6) + '.' + result.slice(-6)).replace(/\.?0+$/, '') || '0';
}
export function mutationKey(): string {
  return (
    'calendar-' +
    Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) =>
      b.toString(16).padStart(2, '0'),
    ).join('')
  );
}
