import {
  ChangeDetectionStrategy,
  Component,
  ElementRef,
  OnDestroy,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { ActivatedRoute, Router, RouterLink, RouterOutlet } from '@angular/router';
import {
  PlantsDocument,
  type PlantCategory,
  type PlantFieldsFragment,
  type PlantFilter,
  type PlantOrder,
} from '@canopy/contracts';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';
import { PageStore, type Page } from '../../core/graphql/page-store';
import { UiIcon } from '../../ui/icon';
import { UiPlantCard } from '../../ui/plant-card';
import { UiSkeleton } from '../../ui/skeleton';

type StatusTab = 'active' | 'archived' | 'dead';

const CATEGORIES: PlantCategory[] = ['CITRUS', 'FIG', 'STRAWBERRY', 'BLACKBERRY', 'OTHER'];
const ORDERS: { value: PlantOrder; label: string }[] = [
  { value: 'NAME_ASC', label: 'Name A–Z' },
  { value: 'NAME_DESC', label: 'Name Z–A' },
  { value: 'ACQUIRED_ON_DESC', label: 'Newest acquisition' },
  { value: 'CREATED_DESC', label: 'Recently added' },
];
/** Field labels are readable text; the option values stay the lower-case
 * statusTabs the URL and the GraphQL filter mapping already use. */
const STATUS_OPTIONS: { value: StatusTab; label: string }[] = [
  { value: 'active', label: 'Unarchived' },
  { value: 'archived', label: 'Archived' },
  { value: 'dead', label: 'Dead' },
];
const VIEW_OPTIONS: { value: 'grid' | 'list'; label: string }[] = [
  { value: 'grid', label: 'Grid' },
  { value: 'list', label: 'List' },
];

/**
 * The collection screen (T10, UI01.b composition): photo-led cards over the
 * real Plants operation, every filter mirrored into the URL so any view is
 * deep-linkable and survives reload. Covers are the plant's own uploaded
 * photos; a plant without one gets the botanical placeholder, never stock
 * imagery. Archive and restore live in the plant edit modal. Empty, error, offline
 * and session states remain distinct: a failed load is never an empty garden.
 */
@Component({
  selector: 'collection-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [RouterLink, RouterOutlet, FormsModule, UiEntryDialog, UiIcon, UiPlantCard, UiSkeleton],
  templateUrl: './collection.html',
  styleUrl: './collection.css',
})
export class CollectionPage implements OnDestroy {
  private readonly gql = inject(GraphQLClient);
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly abort = new AbortController();

  protected readonly categories = CATEGORIES;
  protected readonly orders = ORDERS;
  protected readonly statusOptions = STATUS_OPTIONS;
  protected readonly viewOptions = VIEW_OPTIONS;

  protected readonly q = signal('');
  protected readonly category = signal('');
  protected readonly status = signal<StatusTab>('active');
  protected readonly order = signal<PlantOrder>('NAME_ASC');
  protected readonly view = signal<'grid' | 'list'>('grid');
  protected readonly hasNext = signal(false);

  /** URL-backed filters in a side panel. The panel and its focus trap mount
   * only while open, so initial focus and cleanup follow the panel lifecycle.
   * Close handlers return focus to the Filter trigger. */
  protected readonly filtersOpen = signal(false);
  /** Template refs are elements, not component instances: the button ref is
   * an ElementRef and focus goes through .nativeElement. */
  private readonly filterBtn = viewChild<ElementRef<HTMLButtonElement>>('filterBtn');
  protected draft = { q: '', status: 'active', category: '', order: 'NAME_ASC', view: 'grid' };
  protected readonly activeFilterCount = computed(
    () => (this.q() ? 1 : 0) + (this.status() !== 'active' ? 1 : 0) + (this.category() ? 1 : 0),
  );
  protected readonly filterSummary = computed(() =>
    [
      this.q() ? '“' + this.q() + '”' : '',
      this.category() ? this.category().charAt(0) + this.category().slice(1).toLowerCase() : '',
      this.status() !== 'active' ? this.status() : '',
    ]
      .filter(Boolean)
      .join(' · '),
  );
  protected toggleFilters(): void {
    this.draft = {
      q: this.q(),
      status: this.status(),
      category: this.category(),
      order: this.order(),
      view: this.view(),
    };
    this.filtersOpen.set(true);
  }
  protected resetDraft(): void {
    this.draft = { q: '', status: 'active', category: '', order: 'NAME_ASC', view: 'grid' };
  }
  protected applyCollectionFilters(): void {
    this.setParams({
      q: this.draft.q.trim() || null,
      status: this.draft.status === 'active' ? null : this.draft.status,
      category: this.draft.category || null,
      order: this.draft.order === 'NAME_ASC' ? null : this.draft.order,
      view: this.draft.view === 'grid' ? null : this.draft.view,
    });
    this.closeFilters();
  }
  protected clearAll(): void {
    this.setParams({ q: null, status: null, category: null, order: null, view: null });
  }

  protected closeFilters(): void {
    this.filtersOpen.set(false);
    this.focusFilterBtn();
  }

  protected focusFilterBtn(): void {
    this.filterBtn()?.nativeElement.focus();
  }

  protected readonly store: PageStore<PlantFieldsFragment>;

  constructor() {
    this.store = new PageStore((after) => this.fetchPage(after));
    this.route.queryParamMap.subscribe((pm) => {
      this.q.set(pm.get('q') ?? '');
      this.category.set(
        CATEGORIES.includes(pm.get('category') as PlantCategory) ? pm.get('category')! : '',
      );
      this.status.set(
        STATUS_OPTIONS.some((s) => s.value === pm.get('status'))
          ? (pm.get('status') as StatusTab)
          : 'active',
      );
      this.order.set(
        ORDERS.some((o) => o.value === pm.get('order'))
          ? (pm.get('order') as PlantOrder)
          : 'NAME_ASC',
      );
      this.view.set(pm.get('view') === 'list' ? 'list' : 'grid');
      void this.store.refresh();
    });
  }

  ngOnDestroy(): void {
    this.abort.abort();
    this.store.destroy();
  }

  protected listLike(): boolean {
    return this.view() === 'list';
  }

  /** A successful load with no rows, and nothing narrowing them: the garden
   * really is empty. Anything else that comes back empty is filtered empty. */
  protected isTrueEmpty(): boolean {
    return (
      !this.store.error() &&
      !this.store.loading() &&
      this.store.items().length === 0 &&
      this.status() === 'active' &&
      !this.category() &&
      !this.q()
    );
  }

  protected setParams(patch: Record<string, string | null>): void {
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: patch,
      queryParamsHandling: 'merge',
    });
  }

  protected retry(): void {
    void this.store.refresh();
  }

  /** The form modal renders in this page's outlet (nested in a later pass).
   * When it deactivates — saved or dismissed — the list re-reads, so a new or
   * edited plant shows immediately. */
  protected onOutletDeactivate(): void {
    void this.store.refresh();
  }

  protected coverUrl(p: PlantFieldsFragment): string | null {
    return p.coverPhoto ? `/api/media/${p.coverPhoto.asset.id}/medium` : null;
  }

  protected meta(p: PlantFieldsFragment): string {
    const parts = [p.currentContext?.location?.name ?? 'Location not set'];
    if (p.currentContext) parts.push(p.currentContext.placement.toLowerCase());
    if (p.type === 'GROUP')
      parts.push(p.count != null ? `group (${p.count})` : 'group · count unknown');
    return parts.join(' · ');
  }

  /** Archived/Dead take precedence; otherwise the plant's own recorded category */
  protected badge(p: PlantFieldsFragment): string {
    if (p.status === 'ARCHIVED') return 'Archived';
    if (p.status === 'DEAD') return 'Dead';
    return p.category ? p.category.toLowerCase() : '';
  }

  /** unknown cultivar stays honest, as quiet metadata rather than a badge */
  protected quiet(p: PlantFieldsFragment): string {
    return p.cultivar ? '' : 'Cultivar unknown';
  }

  protected since(p: PlantFieldsFragment): string {
    const date = p.acquiredOn ?? p.createdAt.slice(0, 10);
    const label = p.acquiredOn ? 'Acquired' : 'Added';
    const d = new Date(`${date}T00:00:00`);
    if (Number.isNaN(d.getTime())) return label;
    return `${label} ${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}`;
  }

  private fetchPage(after: string | null): Promise<GqlResult<Page<PlantFieldsFragment>>> {
    // first:20 is a product choice — a comfortable mobile page. The server
    // charges the SDL default page size for complexity regardless of this
    // variable, and its budget is sized to fit this frozen operation.
    const filter: PlantFilter = {
      ...(this.q() ? { query: this.q() } : {}),
      ...(this.category() ? { category: this.category() as PlantCategory } : {}),
      status:
        this.status() === 'archived'
          ? ['ARCHIVED']
          : this.status() === 'dead'
            ? ['DEAD']
            : ['ACTIVE', 'DEAD'], // contract default: archived only when asked
    };
    return this.gql
      .query(PlantsDocument, { first: 20, after, filter, order: this.order() }, this.abort.signal)
      .then((res) => {
        if (res.kind !== 'ok') return res;
        if (!res.data) return { kind: 'ok', data: null, errors: res.errors };
        const conn = res.data.plants;
        this.hasNext.set(conn.pageInfo.hasNextPage);
        return {
          kind: 'ok',
          errors: res.errors,
          data: {
            items: conn.items,
            endCursor: conn.pageInfo.endCursor,
            hasNextPage: conn.pageInfo.hasNextPage,
          },
        };
      });
  }
}
