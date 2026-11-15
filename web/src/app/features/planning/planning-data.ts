import { Injectable, inject } from '@angular/core';
import type { TypedDocumentNode } from '@graphql-typed-document-node/core';
import {
  LocationsDocument,
  SessionDocument,
  PlantingBoardDocument,
  PlantingPlanDetailDocument,
  PlantingScheduleDocument,
  type PlantingBoardQuery,
  type PlantingPlanDetailQuery,
  type PlantingPlanFilter,
  type PlantingPeriodInput,
  type PlantingScheduleInput,
  type PlantingScheduleQuery,
} from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { requestId } from '../../core/request-id';
import { resultData } from '../calendar/calendar-data';
import type { PayloadProblem } from '../collection/shared';
import type { UiIconName } from '../../ui/icon';

export type Board = PlantingBoardQuery['plantingBoard'];
export type PlanRow = Board['items'][number];
export type Plan = PlanRow['plan'];
export type Detail = NonNullable<PlantingPlanDetailQuery['plantingPlan']>;
export type Schedule = NonNullable<PlantingScheduleQuery['plantingSchedule']>;
export type ProjectedWindow = PlanRow['schedule']['items'][number];
export type Period = NonNullable<Plan['targetPeriod']>;
export type CommandPayload = { plan: Plan | null; problems: PayloadProblem[] };

@Injectable()
export class PlanningApi {
  private readonly gql = inject(GraphQLClient);
  async context(signal?: AbortSignal) {
    const [session, places] = await Promise.all([
      this.gql.query(SessionDocument, {}, signal),
      this.gql.query(LocationsDocument, {}, signal),
    ]);
    const workspace = resultData(session).session.workspace;
    if (!workspace) throw new Error('Garden settings could not be loaded. Please retry.');
    return { workspace, locations: resultData(places).locations };
  }
  async board(
    input: PlantingScheduleInput,
    filter: PlantingPlanFilter,
    after: string | null,
    signal?: AbortSignal,
  ): Promise<Board> {
    return resultData(
      await this.gql.query(PlantingBoardDocument, { input, filter, first: 25, after }, signal),
    ).plantingBoard;
  }
  async detail(id: string, signal?: AbortSignal): Promise<Detail> {
    const plan = resultData(
      await this.gql.query(PlantingPlanDetailDocument, { id }, signal),
    ).plantingPlan;
    if (!plan) throw new Error('This plan is no longer available. Reload your plans.');
    return plan;
  }
  async schedule(
    planId: string,
    input: PlantingScheduleInput,
    after: string | null = null,
    signal?: AbortSignal,
  ): Promise<Schedule> {
    const schedule = resultData(
      await this.gql.query(PlantingScheduleDocument, { planId, input, first: 100, after }, signal),
    ).plantingSchedule;
    if (!schedule) throw new Error('This plan is no longer available.');
    return schedule;
  }
  async command<T, V>(
    doc: TypedDocumentNode<T, V>,
    variables: V,
    select: (data: T) => CommandPayload,
  ): Promise<CommandPayload> {
    return select(resultData(await this.gql.mutate(doc, variables)));
  }
}

// One editor/explicit action owns one attempt. Ambiguous network retries reuse
// the key; a changed payload or a newly opened editor gets a fresh intention.
export class PlanningAttempt {
  private payload = '';
  private key = '';
  for(input: unknown): string {
    const payload = JSON.stringify(input);
    if (payload !== this.payload || !this.key) {
      this.payload = payload;
      this.key = requestId();
    }
    return this.key;
  }
}
export const methodLabels: Record<string, string> = {
  START_INDOORS: 'Start seeds indoors',
  DIRECT_SOW: 'Sow directly',
  NURSERY_PLANT: 'Nursery plant',
};
export const phaseLabels: Record<string, string> = {
  START_INDOORS: 'Start indoors',
  DIRECT_SOW: 'Sow directly',
  TRANSPLANT: 'Transplant',
  PLANT_OUT: 'Plant outside',
  GROWTH: 'Growth',
  BLOOM: 'Bloom',
  HARVEST: 'Harvest',
  DORMANCY: 'Dormancy',
  PROTECT: 'Protect',
};
export const stateLabels: Record<string, string> = {
  READY: 'Guidance ready',
  PENDING: 'Research queued',
  PROCESSING: 'Reading sources',
  NEEDS_LOCATION: 'Add growing region',
  DISABLED: 'Plan your own dates',
  UNSUPPORTED: 'No regional guide yet',
  FAILED: 'Research needs a retry',
};
export function phaseIcon(phase: string): UiIconName {
  return (
    (
      {
        BLOOM: 'flower',
        HARVEST: 'fruit',
        DORMANCY: 'moon',
        PROTECT: 'shield',
        TRANSPLANT: 'leaf',
        PLANT_OUT: 'leaf',
      } as Record<string, UiIconName>
    )[phase] ?? 'sprout'
  );
}
export function monthIndex(value: string): number {
  const [year, month] = value.split('-').map(Number);
  return year * 12 + month - 1;
}
export function monthAt(index: number): string {
  return `${Math.floor(index / 12)
    .toString()
    .padStart(4, '0')}-${((index % 12) + 1).toString().padStart(2, '0')}`;
}
export function validViewMonth(value: string | null): value is string {
  return (
    !!value &&
    /^(?!0000)\d{4}-(0[1-9]|1[0-2])$/.test(value) &&
    monthIndex(value) <= monthIndex('9999-01')
  );
}
export function dateLabel(value: string, options: Intl.DateTimeFormatOptions = {}): string {
  return new Intl.DateTimeFormat('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    ...options,
    timeZone: 'UTC',
  }).format(new Date(`${value}T12:00:00Z`));
}
export function monthLabel(value: string, full = false): string {
  return dateLabel(value + '-01', { month: full ? 'long' : 'short', day: undefined });
}
export function periodLabel(period: Period | PlantingPeriodInput | null | undefined): string {
  if (!period) return 'Dates to choose';
  if (period.dates)
    return period.dates.from === period.dates.to
      ? dateLabel(period.dates.from)
      : `${dateLabel(period.dates.from)} – ${dateLabel(period.dates.to)}`;
  if (period.months)
    return period.months.from === period.months.to
      ? monthLabel(period.months.from)
      : `${monthLabel(period.months.from)} – ${monthLabel(period.months.to)}`;
  if (period.season)
    return `${period.season.season.charAt(0) + period.season.season.slice(1).toLowerCase()} ${period.season.year}`;
  return `${period.year}`;
}
export function windowBands(row: PlanRow, fromMonth: string) {
  // Identical visual bands can have several source notes. Group the drawing,
  // retaining every note in detail; grouped clicks open the full review list.
  const groups = new Map<string, { window: ProjectedWindow; count: number }>();
  for (const window of row.schedule.items.filter((w) => !!w.displayMonths)) {
    const key = JSON.stringify([
      window.phase,
      window.displayMonths?.from,
      window.displayMonths?.to,
      window.estimated,
      window.firstHarvestUnknown,
    ]);
    const existing = groups.get(key);
    if (existing) existing.count++;
    else groups.set(key, { window, count: 1 });
  }
  const lanes: number[] = [];
  return [...groups.values()].map(({ window, count }) => {
    const start = monthIndex(window.displayMonths!.from) - monthIndex(fromMonth);
    const end = monthIndex(window.displayMonths!.to) - monthIndex(fromMonth);
    let lane = lanes.findIndex((last) => last < start);
    if (lane < 0) lane = lanes.length;
    lanes[lane] = end;
    return { window, count, start, width: end - start + 1, lane };
  });
}
