import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { Router } from '@angular/router';
import { SessionDocument, type SessionQuery } from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient } from '../../core/graphql/client';
import { UiTheme } from '../../ui/theme';
import { AiStatusPanel } from './ai-status';
import { AgentConnections } from './agents/agents';
import { AccountSettings } from './accounts';
import { GardenSettings } from '../seasons/garden-settings';

type WorkspaceView = SessionQuery['session']['workspace'];

/**
 * Settings basics: theme (fully wired), workspace profile read through the
 * typed GraphQL transport with explicit unknown states until loaded or when
 * genuinely unset, and session controls. Editing lands with the collection
 * lane (T10).
 */
@Component({
  selector: 'settings-page',
  imports: [GardenSettings, AiStatusPanel, AgentConnections, AccountSettings],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './settings.html',
  styleUrl: './settings.css',
})
export class SettingsPage {
  protected readonly theme = inject(UiTheme);
  protected readonly themeOptions = ['light', 'dark', 'system'] as const;
  protected readonly workspace = signal<WorkspaceView | null>(null);

  private readonly session = inject(SessionService);
  private readonly router = inject(Router);
  private readonly gql = inject(GraphQLClient);

  constructor() {
    this.reloadWorkspace();
  }

  protected isAdmin(): boolean {
    return this.session.info()?.user?.role === 'ADMIN';
  }

  protected reloadWorkspace(): void {
    // One-shot read through the typed transport (generated SessionDocument
    // → real gqlgen server). Editing opens with workspace settings sync.
    void this.gql.query(SessionDocument).then((res) => {
      if (res.kind === 'ok' && res.data) this.workspace.set(res.data.session.workspace);
    });
  }

  protected isSelected(opt: (typeof this.themeOptions)[number]): boolean {
    if (opt === 'system') return !localStorage.getItem('canopy-theme');
    return this.theme.theme() === opt;
  }

  protected shortWorkspace(): string {
    return this.session.info()?.workspaceId.slice(0, 8) ?? '—';
  }

  protected expiresLabel(): string {
    const raw = this.session.info()?.expiresAt;
    if (!raw) return 'unknown';
    return new Date(raw).toLocaleString(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short',
    });
  }

  protected async signOut(): Promise<void> {
    await this.session.logout();
    await this.router.navigateByUrl('/login');
  }
}
