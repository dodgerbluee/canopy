import { PlantingEventsDocument, type PlantingEventsQuery } from '@canopy/contracts';
import type { GraphQLClient } from '../../core/graphql/client';
import { resultData } from '../calendar/calendar-data';

export type PlanningEvent = PlantingEventsQuery['plantingEvents']['items'][number];
export async function planningEvents(
  gql: GraphQLClient,
  from: string,
  to: string,
  signal: AbortSignal,
): Promise<PlanningEvent[]> {
  const items = new Map<string, PlanningEvent>();
  const seen = new Set<string>();
  let after: string | null = null;
  for (;;) {
    const page: PlantingEventsQuery['plantingEvents'] = resultData(
      await gql.query(PlantingEventsDocument, { from, to, first: 100, after }, signal),
    ).plantingEvents;
    for (const item of page.items) items.set(item.id, item);
    if (!page.pageInfo.hasNextPage) return [...items.values()];
    after = page.pageInfo.endCursor;
    if (!after || seen.has(after))
      throw new Error('Planning dates could not be loaded completely. Please retry.');
    seen.add(after);
  }
}
