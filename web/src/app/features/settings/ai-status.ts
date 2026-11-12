import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import {
  AIConnectionsDocument,
  SaveAIConnectionDocument,
  SaveAIFeatureBindingsDocument,
  DeleteAIConnectionDocument,
  TestAIConnectionDocument,
  type AIConnectionSettingsFieldsFragment,
  type AIConnectionFieldsFragment,
  type AIConnectionFeature,
  type AIProviderProtocol,
  type SaveAIConnectionInput,
  type TestAIConnectionMutation,
} from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { requestId } from '../../core/request-id';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { resultData } from '../calendar/calendar-data';
type Settings = AIConnectionSettingsFieldsFragment;
type Connection = AIConnectionFieldsFragment;
type TestResult = NonNullable<TestAIConnectionMutation['testAIConnection']['result']>;
const labels: Record<AIConnectionFeature, string> = {
  GARDEN: 'Garden questions & research',
  VISION: 'Plant photo analysis',
  RECEIPT: 'Receipt extraction',
};
@Component({
  selector: 'ai-connection-status',
  imports: [FormsModule, RouterLink, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './ai-status.html',
  styleUrl: './ai-status.css',
})
export class AiStatusPanel {
  private readonly gql = inject(GraphQLClient);
  private readonly abort = new AbortController();
  private readonly keys = new Map<string, string>();
  readonly editor = viewChild<UiEntryDialog>('editor');
  readonly assignmentDialog = viewChild<UiEntryDialog>('assignmentDialog');
  readonly data = signal<Settings | null>(null);
  readonly error = signal('');
  readonly formError = signal('');
  readonly busy = signal(false);
  readonly open = signal(false);
  readonly assignmentsOpen = signal(false);
  readonly deleting = signal<Connection | null>(null);
  readonly result = signal<TestResult | null>(null);
  readonly notice = signal('');
  readonly features: AIConnectionFeature[] = ['GARDEN', 'VISION', 'RECEIPT'];
  readonly labels = labels;
  readonly protocols: { value: AIProviderProtocol; label: string }[] = [
    { value: 'OPENAI_COMPATIBLE', label: 'OpenAI compatible · Strata, OpenAI, Ollama, LM Studio' },
    { value: 'ANTHROPIC', label: 'Anthropic · Claude' },
    { value: 'GEMINI', label: 'Google · Gemini' },
  ];
  draft: SaveAIConnectionInput = this.blank();
  apiKey = '';
  clearKey = false;
  hasKey = false;
  testVision = false;
  choices: Record<AIConnectionFeature, string> = {
    GARDEN: 'DEFAULT',
    VISION: 'DEFAULT',
    RECEIPT: 'DEFAULT',
  };
  private copyFrom: AIConnectionFeature | null = null;
  private copyEndpoint = '';
  private copyProtocol: AIProviderProtocol = 'OPENAI_COMPATIBLE';
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.abort.abort();
      this.apiKey = '';
      this.keys.clear();
    });
    void this.load();
  }
  private blank(): SaveAIConnectionInput {
    return {
      id: '',
      name: '',
      protocol: 'OPENAI_COMPATIBLE',
      endpoint: '',
      model: '',
      deploymentLocal: false,
      supportsVision: false,
      externalConsent: false,
      reasoningEffort: '',
      tokenLimitField: 'MAX_TOKENS',
      expectedRevision: 0,
    };
  }
  async load(): Promise<void> {
    this.error.set('');
    try {
      const d = resultData(await this.gql.query(AIConnectionsDocument, {}, this.abort.signal));
      if (!this.abort.signal.aborted) this.data.set(d.aiConnections);
    } catch {
      if (!this.abort.signal.aborted) this.error.set('AI connections could not be loaded.');
    }
  }
  edit(c?: Connection): void {
    this.reset();
    this.draft = c
      ? {
          id: c.id,
          name: c.name,
          protocol: c.protocol,
          endpoint: c.endpoint,
          model: c.model,
          deploymentLocal: c.deploymentLocal,
          supportsVision: c.supportsVision,
          externalConsent: c.externalConsent,
          reasoningEffort: c.reasoningEffort,
          tokenLimitField: c.tokenLimitField,
          expectedRevision: c.revision,
        }
      : this.blank();
    if (!c) this.draft.id = requestId();
    this.hasKey = c?.hasAPIKey ?? false;
    this.open.set(true);
  }
  copy(d: Settings['deploymentDefaults'][number]): void {
    this.edit();
    this.draft = {
      ...this.draft,
      name: d.model + ' · my connection',
      protocol: d.protocol,
      endpoint: d.endpoint,
      model: d.model,
      deploymentLocal: d.deploymentLocal,
      supportsVision: d.supportsVision,
      externalConsent: false,
      reasoningEffort: d.reasoningEffort,
      tokenLimitField: d.tokenLimitField,
    };
    this.copyFrom = d.feature;
    this.copyEndpoint = d.endpoint;
    this.copyProtocol = d.protocol;
    this.hasKey = d.hasAPIKey;
  }
  private reset(): void {
    this.apiKey = '';
    this.clearKey = false;
    this.hasKey = false;
    this.copyFrom = null;
    this.testVision = false;
    this.formError.set('');
    this.notice.set('');
    this.result.set(null);
    this.keys.clear();
  }
  close(): void {
    this.open.set(false);
    this.apiKey = '';
    this.copyFrom = null;
    this.keys.clear();
  }
  protocolChanged(value: AIProviderProtocol): void {
    this.draft.protocol = value;
    this.draft.reasoningEffort = '';
    this.result.set(null);
  }
  private input(): SaveAIConnectionInput {
    const input = {
      ...this.draft,
      endpoint: this.draft.endpoint.trim().replace(/\/+$/, ''),
      clearAPIKey: this.clearKey,
    };
    if (this.apiKey.trim()) input.apiKey = this.apiKey.trim();
    else delete input.apiKey;
    if (
      !this.apiKey.trim() &&
      !this.clearKey &&
      this.copyFrom &&
      input.endpoint === this.copyEndpoint &&
      input.protocol === this.copyProtocol
    )
      input.copyDeploymentKeyFrom = this.copyFrom;
    else delete input.copyDeploymentKeyFrom;
    return input;
  }
  // Request identities are memory-only and keyed by the exact intended input.
  private key(operation: string, input: unknown): string {
    const signature = operation + JSON.stringify(input);
    let key = this.keys.get(signature);
    if (!key) {
      key = requestId();
      this.keys.set(signature, key);
    }
    return key;
  }
  private problems(problems: { message: string }[]): void {
    if (problems.length) throw Error(problems.map((p) => p.message).join(' '));
  }
  async save(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    this.formError.set('');
    try {
      const input = this.input();
      const p = resultData(
        await this.gql.mutate(
          SaveAIConnectionDocument,
          { input, clientMutationId: this.key('save', input) },
          this.abort.signal,
        ),
      ).saveAIConnection;
      this.problems(p.problems);
      if (!p.connection) throw Error('The connection was not saved.');
      if (this.abort.signal.aborted) return;
      this.editor()?.markPristine();
      this.close();
      this.notice.set('Connection saved. Choose its features to start using it.');
      await this.load();
    } catch (e) {
      if (!this.abort.signal.aborted)
        this.formError.set(e instanceof Error ? e.message : 'Connection could not be saved.');
    } finally {
      this.busy.set(false);
    }
  }
  async test(c?: Connection): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    this.formError.set('');
    this.error.set('');
    this.result.set(null);
    try {
      const input = c
        ? { id: c.id, expectedRevision: c.revision, testVision: false }
        : { draft: this.input(), testVision: this.testVision && this.draft.supportsVision };
      const p = resultData(
        await this.gql.mutate(TestAIConnectionDocument, { input }, this.abort.signal),
      ).testAIConnection;
      this.problems(p.problems);
      if (!p.result) throw Error('No test result was returned.');
      if (this.abort.signal.aborted) return;
      this.result.set(p.result);
      if (c) await this.load();
    } catch (e) {
      if (!this.abort.signal.aborted)
        (c ? this.error : this.formError).set(
          e instanceof Error ? e.message : 'Connection test failed.',
        );
    } finally {
      this.busy.set(false);
    }
  }
  assignments(): void {
    this.formError.set('');
    this.keys.clear();
    for (const b of this.data()?.bindings ?? [])
      this.choices[b.feature] = b.mode === 'CONNECTION' ? (b.connectionId ?? 'DISABLED') : b.mode;
    this.assignmentsOpen.set(true);
  }
  async saveAssignments(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    this.formError.set('');
    try {
      const input = this.features.map((feature) => {
        const value = this.choices[feature];
        return {
          feature,
          mode:
            value === 'DEFAULT'
              ? ('DEFAULT' as const)
              : value === 'DISABLED'
                ? ('DISABLED' as const)
                : ('CONNECTION' as const),
          connectionId: value === 'DEFAULT' || value === 'DISABLED' ? null : value,
          expectedRevision: this.data()?.bindings.find((b) => b.feature === feature)?.revision ?? 0,
        };
      });
      const p = resultData(
        await this.gql.mutate(
          SaveAIFeatureBindingsDocument,
          { input, clientMutationId: this.key('bindings', input) },
          this.abort.signal,
        ),
      ).saveAIFeatureBindings;
      this.problems(p.problems);
      if (!p.settings) throw Error('Feature choices were not saved.');
      if (this.abort.signal.aborted) return;
      this.data.set(p.settings);
      this.assignmentDialog()?.markPristine();
      this.assignmentsOpen.set(false);
      this.notice.set('AI feature choices saved. New requests use these connections.');
    } catch (e) {
      if (!this.abort.signal.aborted)
        this.formError.set(e instanceof Error ? e.message : 'Feature choices could not be saved.');
    } finally {
      this.busy.set(false);
    }
  }
  async remove(): Promise<void> {
    const c = this.deleting();
    if (!c || this.busy()) return;
    this.busy.set(true);
    this.formError.set('');
    try {
      const input = { id: c.id, expectedRevision: c.revision };
      const p = resultData(
        await this.gql.mutate(
          DeleteAIConnectionDocument,
          { input, clientMutationId: this.key('delete', input) },
          this.abort.signal,
        ),
      ).deleteAIConnection;
      this.problems(p.problems);
      if (!p.settings) throw Error('Connection was not removed.');
      if (this.abort.signal.aborted) return;
      this.data.set(p.settings);
      this.deleting.set(null);
      this.notice.set('Connection removed. Features assigned to it are turned off.');
    } catch (e) {
      if (!this.abort.signal.aborted)
        this.formError.set(e instanceof Error ? e.message : 'Connection could not be removed.');
    } finally {
      this.busy.set(false);
    }
  }
  bindingLabel(feature: AIConnectionFeature): string {
    const s = this.data(),
      b = s?.bindings.find((b) => b.feature === feature);
    if (b?.mode === 'DISABLED') return 'Off';
    if (b?.mode === 'CONNECTION')
      return s?.connections.find((c) => c.id === b.connectionId)?.name ?? 'Connection unavailable';
    const d = s?.deploymentDefaults.find((d) => d.feature === feature);
    return d
      ? 'Deployment default · ' + d.model
      : feature === 'RECEIPT'
        ? 'Deployment default · local OCR'
        : 'Deployment default · no chat model';
  }
}
