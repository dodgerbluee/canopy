import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import {
  CancelPlantingPlanDocument,
  RestorePlantingPlanDocument,
  ChangePlantingPlanStateDocument,
  RefreshPlantingGuidanceDocument,
  type PlantingPlanFilter,
  type PlantingPlanState,
} from '@canopy/contracts';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { UiIcon } from '../../ui/icon';
import { UiTabs, UiTab } from '../../ui/tabs';
import { UiToasts } from '../../ui/toast';
import { fmtMinor } from '../purchases/money';
import { PlanEditor } from './plan-editor';
import { PlanningAssistant, type ReviewedPlanningNote } from './planning-assistant';
import { GardenEditor } from './garden-editor';
import { ActionCompletion, needsLivingPlant } from './action-completion';
import { ActionEditor } from './action-editor';
import { WindowChooser } from './window-chooser';
import {
  PlanningApi,
  PlanningAttempt,
  monthAt,
  monthIndex,
  monthLabel,
  dateLabel,
  periodLabel,
  windowBands,
  methodLabels,
  stateLabels,
  phaseLabels,
  phaseIcon,
  validViewMonth,
  type Board,
  type PlanRow,
  type Plan,
  type Detail,
  type Schedule,
  type ProjectedWindow,
} from './planning-data';

type Context = Awaited<ReturnType<PlanningApi['context']>>;
type Lifecycle = {
  plan: Plan;
  kind: 'cancel' | 'restore' | PlantingPlanState;
  title: string;
  copy: string;
};
@Component({
  selector: 'garden-planning',
  imports: [
    FormsModule,
    RouterLink,
    UiEntryDialog,
    UiIcon,
    UiTabs,
    UiTab,
    PlanEditor,
    ActionEditor,
    GardenEditor,
    PlanningAssistant,
    ActionCompletion,
    WindowChooser,
  ],
  providers: [PlanningApi],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './planning.html',
  styleUrls: [
    './planning.css',
    './planning-states.css',
    './planning-rows.css',
    './planning-timeline.css',
    './planning-responsive.css',
    './planning-dialogs.css',
  ],
})
export class PlanningPage {
  private readonly api = inject(PlanningApi);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly toasts = inject(UiToasts);
  private alive = true;
  private read?: AbortController;
  private detailRead?: AbortController;
  private generation = 0;
  private detailGeneration = 0;
  private poll?: ReturnType<typeof setTimeout>;
  private attempt = new PlanningAttempt();
  readonly compact = signal(false);
  private media?: MediaQueryList;
  private readonly resized = (event: MediaQueryListEvent) => this.compact.set(event.matches);
  private readonly visible = () => {
    if (
      !document.hidden &&
      this.board()?.items.some((row) =>
        ['PENDING', 'PROCESSING'].includes(row.schedule.guidanceState),
      )
    )
      void this.load(true);
  };
  readonly context = signal<Context | null>(null);
  readonly board = signal<Board | null>(null);
  readonly loading = signal(true);
  readonly error = signal('');
  readonly view = signal<'planned' | 'wishlist'>('planned');
  readonly from = signal('');
  readonly query = signal('');
  readonly method = signal('');
  readonly location = signal('');
  readonly history = signal('current');
  readonly after = signal<string | null>(null);
  readonly to = computed(() => (this.from() ? monthAt(monthIndex(this.from()) + 11) : ''));
  readonly months = computed(() =>
    this.from()
      ? Array.from({ length: 12 }, (_, i) => {
          const value = monthAt(monthIndex(this.from()) + i);
          return { value, label: monthLabel(value).split(' ')[0], year: value.slice(0, 4) };
        })
      : [],
  );
  readonly rangeLabel = computed(() =>
    this.from()
      ? `${monthLabel(this.from())} – ${monthLabel(this.to())}`
      : 'Your next twelve months',
  );
  readonly rows = computed(() => {
    const items = [...(this.board()?.items ?? [])];
    if (this.compact())
      items.sort(
        (a, b) =>
          (a.schedule.targetDisplayMonths?.from ?? '9999-99').localeCompare(
            b.schedule.targetDisplayMonths?.from ?? '9999-99',
          ) || a.plan.name.localeCompare(b.plan.name),
      );
    return items.map((row) => {
      const bands = windowBands(row, this.from());
      return {
        row,
        bands: bands.filter((b) => b.lane < 3),
        more: bands.filter((b) => b.lane >= 3).length,
      };
    });
  });
  readonly filterCount = computed(
    () =>
      Number(!!this.query()) +
      Number(!!this.method()) +
      Number(!!this.location()) +
      Number(this.history() !== 'current'),
  );
  readonly nextAction = computed(
    () =>
      this.board()
        ?.items.flatMap((r) =>
          r.plan.actions
            .filter((a) => a.state === 'OPEN')
            .map((action) => ({ plan: r.plan, action })),
        )
        .sort((a, b) => a.action.scheduledOn.localeCompare(b.action.scheduledOn))[0] ?? null,
  );
  readonly methodLabels = methodLabels;
  readonly stateLabels = stateLabels;
  readonly phaseLabels = phaseLabels;
  readonly phaseIcon = phaseIcon;
  readonly periodLabel = periodLabel;
  readonly dateLabel = dateLabel;
  readonly fmtMinor = fmtMinor;
  readonly filtersOpen = signal(false);
  readonly rangeOpen = signal(false);
  filterDraft = { query: '', method: '', location: '', history: 'current' };
  rangeDraft = '';
  readonly editorOpen = signal(false);
  readonly editPlan = signal<Plan | null>(null);
  readonly detailOpen = signal(false);
  readonly detail = signal<Detail | null>(null);
  readonly detailSchedule = signal<Schedule | null>(null);
  readonly detailLoading = signal(false);
  readonly detailError = signal('');
  readonly detailTab = signal('planning-overview');
  readonly detailDialog = viewChild<UiEntryDialog>('detailDialog');
  readonly actionOpen = signal(false);
  readonly selectedAction = signal<Plan['actions'][number] | null>(null);
  readonly assistantOpen = signal(false);
  readonly assistantPlans = signal<Plan[]>([]);
  readonly suggestedNote = signal<{ text: string; expectedRevision: number } | null>(null);
  readonly gardenOpen = signal(false);
  readonly completionOpen = signal(false);
  readonly completingAction = signal<Plan['actions'][number] | null>(null);
  readonly dismissingAction = signal(false);
  readonly chooserOpen = signal(false);
  readonly selectedWindow = signal<ProjectedWindow | null>(null);
  readonly lifecycle = signal<Lifecycle | null>(null);
  readonly commandBusy = signal(false);
  readonly commandError = signal('');
  readonly refreshing = signal(false);
  constructor() {
    const destroy = inject(DestroyRef);
    if (typeof window.matchMedia === 'function') {
      this.media = window.matchMedia('(max-width:1100px)');
      this.compact.set(this.media.matches);
      this.media.addEventListener?.('change', this.resized);
    }
    document.addEventListener('visibilitychange', this.visible);
    destroy.onDestroy(() => {
      this.alive = false;
      this.read?.abort();
      this.detailRead?.abort();
      document.removeEventListener('visibilitychange', this.visible);
      this.media?.removeEventListener?.('change', this.resized);
      clearTimeout(this.poll);
    });
    this.route.queryParamMap.pipe(takeUntilDestroyed(destroy)).subscribe(() => {
      if (this.context()) this.applyRoute();
    });
    void this.bootstrap();
  }
  async bootstrap(): Promise<void> {
    this.loading.set(true);
    this.error.set('');
    try {
      const context = await this.api.context();
      if (!this.alive) return;
      this.context.set(context);
      this.applyRoute();
    } catch (e) {
      if (this.alive) {
        this.error.set((e as Error).message);
        this.loading.set(false);
      }
    }
  }
  private currentMonth(): string {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: this.context()?.workspace.timezone ?? 'UTC',
      year: 'numeric',
      month: '2-digit',
    }).formatToParts(new Date());
    return `${parts.find((p) => p.type === 'year')!.value.padStart(4, '0')}-${parts.find((p) => p.type === 'month')!.value}`;
  }
  private linkedPlan = '';
  private applyRoute(): void {
    const p = this.route.snapshot.queryParamMap;
    this.view.set(p.get('view') === 'wishlist' ? 'wishlist' : 'planned');
    this.from.set(validViewMonth(p.get('from')) ? p.get('from')! : this.currentMonth());
    this.query.set((p.get('q') ?? '').slice(0, 200));
    this.method.set(
      ['START_INDOORS', 'DIRECT_SOW', 'NURSERY_PLANT'].includes(p.get('method') ?? '')
        ? p.get('method')!
        : '',
    );
    this.location.set(p.get('location') ?? '');
    this.history.set(
      ['completed', 'cancelled', 'all'].includes(p.get('history') ?? '')
        ? p.get('history')!
        : 'current',
    );
    this.after.set(p.get('after'));
    this.board.set(null);
    void this.load();
    const linked = p.get('plan') ?? '';
    if (linked && linked !== this.linkedPlan) {
      this.linkedPlan = linked;
      this.detailOpen.set(true);
      this.detailTab.set('planning-actions');
      void this.fetchDetail(linked);
    }
  }
  private filter(): PlantingPlanFilter {
    const states: PlantingPlanState[] =
      this.history() === 'completed'
        ? ['COMPLETED']
        : this.history() === 'cancelled'
          ? ['CANCELLED']
          : this.history() === 'all'
            ? ['IDEA', 'PLANNED', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED']
            : this.view() === 'wishlist'
              ? ['IDEA']
              : ['PLANNED', 'IN_PROGRESS'];
    return {
      states,
      query: this.query() || null,
      method: (this.method() || null) as PlantingPlanFilter['method'],
      locationId: this.location() || null,
    };
  }
  async load(background = false): Promise<void> {
    const generation = ++this.generation;
    this.read?.abort();
    const request = (this.read = new AbortController());
    clearTimeout(this.poll);
    if (!background) this.loading.set(true);
    this.error.set('');
    try {
      const board = await this.api.board(
        { fromMonth: this.from(), toMonth: this.to() },
        this.filter(),
        this.after(),
        request.signal,
      );
      if (!this.alive || generation !== this.generation) return;
      this.board.set(board);
      const openPlan = this.detail();
      const row = openPlan && board.items.find((item) => item.plan.id === openPlan.id);
      if (
        row &&
        this.detailOpen() &&
        !this.editorOpen() &&
        !this.actionOpen() &&
        !this.chooserOpen() &&
        !this.gardenOpen() &&
        !this.assistantOpen() &&
        !this.completionOpen() &&
        !this.detailLoading() &&
        (row.schedule.guidanceState !== this.detailSchedule()?.guidanceState ||
          row.schedule.guidanceGeneration !== this.detailSchedule()?.guidanceGeneration)
      )
        void this.fetchDetail(openPlan!.id);
      if (board.items.some((r) => ['PENDING', 'PROCESSING'].includes(r.schedule.guidanceState)))
        this.poll = setTimeout(() => {
          if (!document.hidden) void this.load(true);
        }, 8000);
    } catch (e) {
      if (this.alive && generation === this.generation && !request.signal.aborted)
        this.error.set((e as Error).message);
    } finally {
      if (this.alive && generation === this.generation) this.loading.set(false);
    }
  }
  navigate(params: Record<string, string | null>): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { ...params, after: null },
      queryParamsHandling: 'merge',
    });
  }
  selectView(value: string): void {
    this.navigate({ view: value === 'planning-wishlist' ? 'wishlist' : null, history: null });
  }
  move(delta: number): void {
    const next = monthAt(monthIndex(this.from()) + delta);
    if (validViewMonth(next)) this.navigate({ from: next });
  }
  openRange(): void {
    this.rangeDraft = this.from();
    this.rangeOpen.set(true);
  }
  applyRange(): void {
    if (!validViewMonth(this.rangeDraft)) return;
    this.rangeOpen.set(false);
    this.navigate({ from: this.rangeDraft });
  }
  openFilters(): void {
    this.filterDraft = {
      query: this.query(),
      method: this.method(),
      location: this.location(),
      history: this.history(),
    };
    this.filtersOpen.set(true);
  }
  applyFilters(): void {
    this.filtersOpen.set(false);
    this.navigate({
      q: this.filterDraft.query.trim() || null,
      method: this.filterDraft.method || null,
      location: this.filterDraft.location || null,
      history: this.filterDraft.history === 'current' ? null : this.filterDraft.history,
    });
  }
  clearFilters(): void {
    this.navigate({ q: null, method: null, location: null, history: null });
  }
  page(after: string | null): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: { after },
      queryParamsHandling: 'merge',
    });
  }
  create(): void {
    this.suggestedNote.set(null);
    this.editPlan.set(null);
    this.editorOpen.set(true);
  }
  edit(plan: Plan): void {
    this.suggestedNote.set(null);
    this.editPlan.set(plan);
    this.editorOpen.set(true);
  }
  active(plan: Plan): boolean {
    return ['IDEA', 'PLANNED', 'IN_PROGRESS'].includes(plan.state);
  }
  targetStyle(row: PlanRow): { left: string; width: string } {
    const band = row.schedule.targetDisplayMonths;
    return band
      ? {
          left: `${((monthIndex(band.from) - monthIndex(this.from())) / 12) * 100}%`,
          width: `${((monthIndex(band.to) - monthIndex(band.from) + 1) / 12) * 100}%`,
        }
      : { left: '0%', width: '0%' };
  }
  trackHeight(row: PlanRow): number {
    const bands = windowBands(row, this.from());
    return Math.max(108, 58 + Math.min(3, Math.max(-1, ...bands.map((b) => b.lane)) + 1) * 48);
  }
  async openDetail(plan: Plan, tab = 'planning-overview'): Promise<void> {
    if (this.detail()?.id !== plan.id) {
      this.detail.set(null);
      this.detailSchedule.set(null);
    }
    this.detailOpen.set(true);
    this.detailTab.set(tab);
    await this.fetchDetail(plan.id);
  }
  async fetchDetail(id: string): Promise<void> {
    const generation = ++this.detailGeneration;
    this.detailRead?.abort();
    const request = (this.detailRead = new AbortController());
    this.detailLoading.set(true);
    this.detailError.set('');
    try {
      const [plan, schedule] = await Promise.all([
        this.api.detail(id, request.signal),
        this.api.schedule(id, { fromMonth: this.from(), toMonth: this.to() }, null, request.signal),
      ]);
      if (!this.alive || generation !== this.detailGeneration) return;
      this.detail.set(plan);
      this.detailSchedule.set(schedule);
    } catch (e) {
      if (this.alive && generation === this.detailGeneration && !request.signal.aborted)
        this.detailError.set((e as Error).message);
    } finally {
      if (this.alive && generation === this.detailGeneration) this.detailLoading.set(false);
    }
  }
  closeDetail(): void {
    this.detailRead?.abort();
    this.detailGeneration++;
    this.detailOpen.set(false);
    this.detail.set(null);
    this.detailSchedule.set(null);
  }
  async inspect(row: PlanRow, window: ProjectedWindow): Promise<void> {
    await this.openDetail(row.plan, 'planning-windows');
    const fresh = this.detailSchedule()?.items.find(
      (w) =>
        w.factId === window.factId &&
        w.windowIndex === window.windowIndex &&
        w.year === window.year,
    );
    if (fresh) {
      this.selectedWindow.set(fresh);
      this.chooserOpen.set(true);
    } else this.detailError.set('Guidance changed. Review the current windows below.');
  }
  choose(window: ProjectedWindow): void {
    this.selectedWindow.set(window);
    this.chooserOpen.set(true);
  }
  openAction(action: Plan['actions'][number] | null = null): void {
    this.selectedAction.set(action);
    this.actionOpen.set(true);
  }
  hasOpenActions(plan: Plan): boolean {
    return plan.actions.some((action) => action.state === 'OPEN');
  }
  askPlan(plan: Plan | null = null): void {
    this.assistantPlans.set(plan ? [plan] : (this.board()?.items.map((r) => r.plan) ?? []));
    this.assistantOpen.set(true);
  }
  reviewPlanningNote(note: ReviewedPlanningNote): void {
    this.editPlan.set(note.plan);
    this.suggestedNote.set({ text: note.text, expectedRevision: note.expectedRevision });
    this.assistantOpen.set(false);
    this.editorOpen.set(true);
  }
  openGarden(action: Plan['actions'][number] | null = null): void {
    this.completingAction.set(action);
    this.gardenOpen.set(true);
  }
  completeAction(action: Plan['actions'][number], dismiss = false): void {
    this.completingAction.set(action);
    this.dismissingAction.set(dismiss);
    if (!dismiss && needsLivingPlant(action.kind) && !this.detail()?.fulfillment)
      this.gardenOpen.set(true);
    else this.completionOpen.set(true);
  }
  async saved(plan: Plan, reveal = false): Promise<void> {
    this.editorOpen.set(false);
    this.suggestedNote.set(null);
    this.actionOpen.set(false);
    this.chooserOpen.set(false);
    this.gardenOpen.set(false);
    this.completionOpen.set(false);
    this.toasts.ok('Planting plan saved.');
    if (this.detailOpen()) void this.fetchDetail(plan.id);
    if (reveal && !this.editPlan() && this.filterCount()) {
      this.navigate({
        view: plan.state === 'IDEA' ? 'wishlist' : null,
        q: null,
        method: null,
        location: null,
        history: null,
      });
      return;
    }
    if (reveal && !this.editPlan() && plan.state === 'IDEA' && this.view() !== 'wishlist')
      this.navigate({ view: 'wishlist', history: null });
    else if (reveal && !this.editPlan() && plan.state !== 'IDEA' && this.view() === 'wishlist')
      this.navigate({ view: null, history: null });
    else await this.load();
  }
  requestLifecycle(plan: Plan, kind: Lifecycle['kind']): void {
    this.editorOpen.set(false);
    this.commandError.set('');
    this.attempt = new PlanningAttempt();
    const names: Record<string, string> = {
      cancel: 'Cancel this plan?',
      restore: 'Restore this plan?',
      PLANNED: 'Move to planned plantings?',
      IDEA: 'Move to your wishlist?',
      IN_PROGRESS: 'Start this plan?',
      COMPLETED: 'Mark this plan complete?',
    };
    this.lifecycle.set({
      plan,
      kind,
      title: names[kind] ?? 'Update this plan?',
      copy:
        kind === 'cancel'
          ? 'Keep its history and chosen dates. Restore it whenever you are ready.'
          : kind === 'COMPLETED'
            ? 'This marks planning progress as complete. It does not record planting or other garden work.'
            : 'Your dates, actions and source history stay with this plan.',
    });
  }
  async applyLifecycle(): Promise<void> {
    const request = this.lifecycle();
    if (!request || this.commandBusy()) return;
    this.commandBusy.set(true);
    this.commandError.set('');
    try {
      const input = { id: request.plan.id, expectedRevision: request.plan.revision };
      const key = this.attempt.for({ ...input, kind: request.kind });
      const payload =
        request.kind === 'cancel'
          ? await this.api.command(
              CancelPlantingPlanDocument,
              { input, clientMutationId: key },
              (d) => d.cancelPlantingPlan,
            )
          : request.kind === 'restore'
            ? await this.api.command(
                RestorePlantingPlanDocument,
                { input, clientMutationId: key },
                (d) => d.restorePlantingPlan,
              )
            : await this.api.command(
                ChangePlantingPlanStateDocument,
                { input: { ...input, state: request.kind }, clientMutationId: key },
                (d) => d.changePlantingPlanState,
              );
      if (payload.problems.length) {
        this.commandError.set(payload.problems.map((p) => p.message).join(' '));
        return;
      }
      if (!payload.plan)
        throw new Error('The server did not confirm this change. Retry with the same values.');
      this.lifecycle.set(null);
      this.closeDetail();
      await this.saved(payload.plan);
    } catch (e) {
      this.commandError.set((e as Error).message);
    } finally {
      this.commandBusy.set(false);
    }
  }
  async refreshGuidance(): Promise<void> {
    const plan = this.detail();
    if (!plan || this.refreshing()) return;
    this.refreshing.set(true);
    this.detailError.set('');
    try {
      const input = { id: plan.id, expectedRevision: plan.revision };
      const payload = await this.api.command(
        RefreshPlantingGuidanceDocument,
        { input, clientMutationId: this.attempt.for({ ...input, kind: 'refresh' }) },
        (d) => d.refreshPlantingGuidance,
      );
      if (payload.problems.length) {
        this.detailError.set(payload.problems.map((p) => p.message).join(' '));
        return;
      }
      await this.fetchDetail(plan.id);
      void this.load(true);
      this.toasts.ok('Regional research requested.');
    } catch (e) {
      this.detailError.set((e as Error).message);
    } finally {
      this.refreshing.set(false);
    }
  }
  async moreWindows(): Promise<void> {
    const plan = this.detail(),
      page = this.detailSchedule();
    if (!plan || !page?.pageInfo.endCursor || this.detailLoading()) return;
    this.detailLoading.set(true);
    try {
      const next = await this.api.schedule(
        plan.id,
        { fromMonth: this.from(), toMonth: this.to() },
        page.pageInfo.endCursor,
      );
      this.detailSchedule.set({ ...next, items: [...page.items, ...next.items] });
    } catch (e) {
      this.detailError.set((e as Error).message);
    } finally {
      this.detailLoading.set(false);
    }
  }
}
