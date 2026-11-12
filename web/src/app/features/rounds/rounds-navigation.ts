import { inject, Injectable } from '@angular/core';
import { SessionService } from '../../core/auth/session';

/** In-memory return position; uploads and saved records live elsewhere. */
@Injectable({ providedIn: 'root' })
export class RoundNavigation {
  private readonly session = inject(SessionService);
  private point: { plantId: string; scrollY: number; workspace: string } | null = null;

  remember(plantId: string): void {
    const workspace = this.session.info()?.workspaceId;
    if (workspace) this.point = { plantId, scrollY: window.scrollY, workspace };
  }

  takeReturn(): { plantId: string; scrollY: number } | null {
    const point = this.point;
    this.point = null;
    return point?.workspace === this.session.info()?.workspaceId ? point : null;
  }
}
