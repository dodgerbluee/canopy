import { PlantsDocument, type PlantsQuery, type PlantFieldsFragment } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { resultData } from '../calendar/calendar-data';
export async function loadGardenPlants(
  gql: GraphQLClient,
  signal: AbortSignal,
  history = false,
): Promise<PlantFieldsFragment[]> {
  const plants = new Map<string, PlantFieldsFragment>();
  const cursors = new Set<string>();
  let after: string | null = null;
  for (;;) {
    const page: PlantsQuery['plants'] = resultData(
      await gql.query(
        PlantsDocument,
        {
          first: 50,
          after,
          filter: { status: history ? ['ACTIVE', 'DEAD', 'ARCHIVED'] : ['ACTIVE'] },
        },
        signal,
      ),
    ).plants;
    for (const plant of page.items) plants.set(plant.id, plant);
    if (!page.pageInfo.hasNextPage) return [...plants.values()];
    const next: string | null | undefined = page.pageInfo.endCursor;
    if (!next || cursors.has(next))
      throw new Error('The plant list could not be fully loaded. Retry.');
    cursors.add(next);
    after = next;
  }
}
