import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  inject,
  signal,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import {
  GardenKnowledgeDocument,
  ProviderCapabilitiesDocument,
  SuggestionsPageDocument,
  type PlantFieldsFragment,
  type ProviderCapabilitiesQuery,
  type SuggestionFieldsFragment,
} from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiIcon } from '../../ui/icon';
import { resultData } from '../calendar/calendar-data';
import { InsightSuggestionCard } from '../insights/suggestion-card';
import { type Garden } from '../seasons/seasons-data';
import { GardenChatDialog } from './garden-chat';
import { loadGardenPlants } from './plants';

@Component({
  selector: 'garden-assistant',
  imports: [RouterLink, UiIcon, GardenChatDialog, InsightSuggestionCard],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './assistant.html',
  styleUrl: './assistant.css',
})
export class AssistantPage {
  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  readonly garden = signal<Garden | null>(null);
  readonly plants = signal<PlantFieldsFragment[]>([]);
  readonly capabilities = signal<ProviderCapabilitiesQuery['providerCapabilities'] | null>(null);
  readonly error = signal('');
  readonly plantError = signal('');
  readonly suggestionsError = signal('');
  readonly loading = signal(true);
  readonly chatOpen = signal(false);
  readonly suggestions = signal<SuggestionFieldsFragment[]>([]);
  readonly more = signal(false);
  readonly suggestionsBusy = signal(false);
  private cursor: string | null = null;
  readonly year = new Date().getFullYear();
  readonly ready = computed(
    () => this.garden()?.plants.filter((p) => p.state === 'READY').length ?? 0,
  );
  constructor() {
    inject(DestroyRef).onDestroy(() => this.abort.abort());
    void this.load();
    void this.loadSuggestions();
  }
  async load(): Promise<void> {
    this.loading.set(true);
    this.error.set('');
    this.plantError.set('');
    await Promise.all([
      this.gql
        .query(GardenKnowledgeDocument, { year: this.year }, this.abort.signal)
        .then((r) => {
          if (!this.abort.signal.aborted) this.garden.set(resultData(r).gardenKnowledge);
        })
        .catch(() => {
          if (!this.abort.signal.aborted)
            this.error.set('Garden guidance could not be loaded. Retry to use garden chat.');
        }),
      loadGardenPlants(this.gql, this.abort.signal)
        .then((p) => {
          if (!this.abort.signal.aborted) this.plants.set(p);
        })
        .catch(() => {
          if (!this.abort.signal.aborted)
            this.plantError.set('Your plants could not be loaded. Retry.');
        }),
      this.gql.query(ProviderCapabilitiesDocument, {}, this.abort.signal).then((r) => {
        if (!this.abort.signal.aborted && r.kind === 'ok' && r.data)
          this.capabilities.set(r.data.providerCapabilities);
      }),
    ]);
    if (!this.abort.signal.aborted) this.loading.set(false);
  }
  async loadSuggestions(append = false): Promise<void> {
    if (this.suggestionsBusy()) return;
    this.suggestionsBusy.set(true);
    this.suggestionsError.set('');
    try {
      const page = resultData(
        await this.gql.query(
          SuggestionsPageDocument,
          { first: 10, after: append ? this.cursor : null, filter: { status: ['PROPOSED'] } },
          this.abort.signal,
        ),
      ).suggestions;
      if (this.abort.signal.aborted) return;
      const all = new Map((append ? this.suggestions() : []).map((s) => [s.id, s]));
      for (const row of page.items) all.set(row.id, row);
      this.suggestions.set([...all.values()]);
      this.more.set(page.pageInfo.hasNextPage);
      this.cursor = page.pageInfo.endCursor;
    } catch {
      if (!this.abort.signal.aborted)
        this.suggestionsError.set('AI suggestions could not be loaded. Retry.');
    } finally {
      if (!this.abort.signal.aborted) this.suggestionsBusy.set(false);
    }
  }
}
