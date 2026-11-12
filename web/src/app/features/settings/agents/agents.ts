import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  AgentSettingsDocument,
  SaveAgentGrantDocument,
  RotateAgentCredentialDocument,
  RevokeAgentGrantDocument,
  DecideAgentActionDocument,
  type AgentSettingsQuery,
  type AgentGrantFieldsFragment,
  type SaveAgentGrantInput,
} from '@canopy/contracts';
import { GraphQLClient } from '../../../core/graphql/client';
import { requestId } from '../../../core/request-id';
import { UiEntryDialog } from '../../../ui/entry-dialog';
import { resultData } from '../../calendar/calendar-data';
import { loadGardenPlants } from '../../assistant/plants';
type Settings = AgentSettingsQuery['agentSettings'];
type Grant = AgentGrantFieldsFragment;
type Action = Settings['actions'][number];
const READ = ['garden:read', 'knowledge:read', 'care:read'];
@Component({
  selector: 'agent-connections',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './agents.html',
  styleUrl: './agents.css',
})
export class AgentConnections {
  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  readonly entry = viewChild<UiEntryDialog>('entry');
  readonly data = signal<Settings | null>(null);
  readonly error = signal('');
  readonly formError = signal('');
  readonly busy = signal(false);
  readonly open = signal(false);
  readonly token = signal('');
  readonly tokenVisible = signal(false);
  readonly reveal = signal(false);
  readonly credentialAction = signal<{ grant: Grant; revoke: boolean } | null>(null);
  readonly review = signal<Action | null>(null);
  readonly plants = signal<{ id: string; name: string }[]>([]);
  readonly picker = signal(false);
  readonly search = signal('');
  readonly notice = signal('');
  draft: SaveAgentGrantInput = {
    id: '',
    name: '',
    scopes: [...READ],
    plantIds: [],
    expiresAt: '',
    expectedRevision: 0,
  };
  expires = '';
  restricted = false;
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.abort.abort();
      this.token.set('');
    });
    void this.load();
  }
  async load(): Promise<void> {
    this.error.set('');
    try {
      const d = resultData(await this.gql.query(AgentSettingsDocument, {}, this.abort.signal));
      if (!this.abort.signal.aborted) this.data.set(d.agentSettings);
    } catch {
      if (!this.abort.signal.aborted) this.error.set('Agent connections could not be loaded.');
    }
  }
  async edit(grant?: Grant): Promise<void> {
    this.formError.set('');
    this.notice.set('');
    this.draft = grant
      ? {
          id: grant.id,
          name: grant.name,
          scopes: [...grant.scopes],
          plantIds: [...grant.plantIds],
          expiresAt: grant.expiresAt,
          expectedRevision: grant.revision,
        }
      : {
          id: requestId(),
          name: '',
          scopes: [...READ],
          plantIds: [],
          expiresAt: new Date(Date.now() + 30 * 86400000).toISOString(),
          expectedRevision: 0,
        };
    this.restricted = this.draft.plantIds.length > 0;
    const d = new Date(this.draft.expiresAt);
    this.expires = new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    this.open.set(true);
    try {
      const ps = await loadGardenPlants(this.gql, this.abort.signal);
      if (!this.abort.signal.aborted) this.plants.set(ps.map((p) => ({ id: p.id, name: p.name })));
    } catch {
      if (!this.abort.signal.aborted)
        this.formError.set(
          'Plant choices could not be loaded. You can retry by reopening this form.',
        );
    }
  }
  toggle(scope: string, enabled: boolean): void {
    this.draft.scopes = enabled
      ? [...this.draft.scopes, scope]
      : this.draft.scopes.filter((s) => s !== scope);
  }
  choosePlant(id: string): void {
    this.draft.plantIds = this.draft.plantIds.includes(id)
      ? this.draft.plantIds.filter((p) => p !== id)
      : [...this.draft.plantIds, id];
  }
  preset(logging: boolean): void {
    this.draft.scopes = [...READ, ...(logging ? ['photos:read', 'logs:write', 'rain:write'] : [])];
  }
  private problems(ps: { message: string }[]): void {
    if (ps.length) throw Error(ps.map((p) => p.message).join(' '));
  }
  private showToken(token: string | null | undefined): void {
    if (token) {
      this.token.set(token);
      this.tokenVisible.set(false);
      this.reveal.set(true);
    }
  }
  async save(): Promise<void> {
    if (this.busy()) return;
    this.formError.set('');
    if (this.restricted && this.draft.plantIds.length === 0) {
      this.formError.set('Choose at least one plant, or allow the whole garden.');
      return;
    }
    const expiry = new Date(this.expires);
    if (!Number.isFinite(expiry.getTime())) {
      this.formError.set('Choose a valid expiry.');
      return;
    }
    this.busy.set(true);
    try {
      const input = {
        ...this.draft,
        expiresAt: expiry.toISOString(),
        plantIds: this.restricted ? this.draft.plantIds : [],
      };
      const p = resultData(
        await this.gql.mutate(SaveAgentGrantDocument, { input }, this.abort.signal),
      ).saveAgentGrant;
      this.problems(p.problems);
      if (!p.grant) throw Error('Connection was not saved.');
      if (this.abort.signal.aborted) return;
      this.entry()?.markPristine();
      this.open.set(false);
      this.showToken(p.token);
      this.notice.set(
        p.token ? 'Connection created. Save the token shown once below.' : 'Permissions updated.',
      );
      await this.load();
    } catch (e) {
      if (!this.abort.signal.aborted)
        this.formError.set(
          e instanceof Error
            ? e.message
            : 'Could not save. If the reply was lost, reload and rotate the created connection to get a new token.',
        );
    } finally {
      this.busy.set(false);
    }
  }
  async changeCredential(): Promise<void> {
    const action = this.credentialAction();
    if (!action || this.busy()) return;
    this.busy.set(true);
    this.formError.set('');
    try {
      const input = { id: action.grant.id, expectedRevision: action.grant.revision };
      const p = action.revoke
        ? resultData(await this.gql.mutate(RevokeAgentGrantDocument, { input }, this.abort.signal))
            .revokeAgentGrant
        : resultData(
            await this.gql.mutate(RotateAgentCredentialDocument, { input }, this.abort.signal),
          ).rotateAgentCredential;
      this.problems(p.problems);
      if (!p.grant) throw Error('Connection could not be changed.');
      if (this.abort.signal.aborted) return;
      this.credentialAction.set(null);
      this.showToken('token' in p && typeof p.token==='string' ? p.token : null);
      this.notice.set(
        action.revoke
          ? 'Connection revoked. Future agent calls are denied.'
          : 'Old token revoked. Save the replacement token shown once.',
      );
      await this.load();
    } catch (e) {
      if (!this.abort.signal.aborted)
        this.formError.set(e instanceof Error ? e.message : 'Credential change failed.');
    } finally {
      this.busy.set(false);
    }
  }
  async decide(approve: boolean): Promise<void> {
    const action = this.review();
    if (!action || this.busy()) return;
    this.busy.set(true);
    this.formError.set('');
    try {
      const p = resultData(
        await this.gql.mutate(
          DecideAgentActionDocument,
          { input: { id: action.id, approve } },
          this.abort.signal,
        ),
      ).decideAgentAction;
      this.problems(p.problems);
      if (approve && !p.applied) throw Error('The change was not applied.');
      if (this.abort.signal.aborted) return;
      this.review.set(null);
      this.notice.set(approve ? 'Agent change applied.' : 'Agent change rejected.');
      await this.load();
    } catch (e) {
      if (!this.abort.signal.aborted)
        this.formError.set(e instanceof Error ? e.message : 'This proposal could not be applied.');
    } finally {
      this.busy.set(false);
    }
  }
  closeReveal(): void {
    this.reveal.set(false);
    this.token.set('');
    this.tokenVisible.set(false);
  }
  active(grant: Grant): boolean {
    return !grant.revokedAt && new Date(grant.expiresAt).getTime() > Date.now();
  }
  pretty(raw: string): string {
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  }
  date(raw: string): string {
    return new Date(raw).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  }
  filteredPlants(): { id: string; name: string }[] {
    const q = this.search().toLowerCase();
    return this.plants().filter((p) => p.name.toLowerCase().includes(q));
  }
  snippet(): string {
    return `mcp_servers:\n  canopy:\n    url: "${this.data()?.endpoint ?? 'http://localhost:8080/mcp'}"\n    headers:\n      Authorization: "Bearer \u0024{CANOPY_AGENT_TOKEN}"`;
  }
  scopeLabel(scope: string): string {
    return (
      (
        {
          'garden:read': 'Garden & history',
          'knowledge:read': 'Regional knowledge',
          'photos:read': 'Photo previews',
          'logs:write': 'Log watering & observations',
          'rain:write': 'Log rain',
          'plants:write': 'Propose plant changes',
          'care:read': 'Care agenda',
          'care:write': 'Propose care changes',
          'purchases:read': 'Spending & purchases',
          'purchases:write': 'Propose purchase changes',
          'receipts:read': 'Read receipt images',
          'media:write': 'Upload photos & receipts',
          'analysis:run': 'Run AI analysis',
          'suggestions:review': 'Review AI suggestions',
          'inbox:read': 'Read notifications',
          'inbox:write': 'Mark notifications read',
          'exports:request': 'Propose garden exports',
        } as Record<string, string>
      )[scope] ?? scope
    );
  }
}
