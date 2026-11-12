import { Injectable } from '@angular/core';

/**
 * Round-robin picker for the photo-led hero (UI07 owner report: the same plant
 * was always on top). The caller passes the eligible ids in its own order; this
 * service only remembers the last pick per workspace and returns the next one.
 *
 * The last pick lives in nonsecret sessionStorage under 'canopy.hero.' +
 * workspaceId, so a hard refresh also advances from the previous choice. If storage is unavailable (private mode, disabled, SSR) the same
 * sequence is kept in memory for the lifetime of the service. Nothing secret
 * is ever written here.
 *
 * No timers, randomness, network or data-layer imports: construct it directly
 * in a test and call pick().
 */
@Injectable({ providedIn: 'root' })
export class HeroRotation {
  /** last pick per workspace when sessionStorage cannot be reached */
  private readonly memory = new Map<string, string>();

  pick(workspaceId: string, plantIds: readonly string[]): string | null {
    const ids: string[] = [];
    const seen = new Set<string>();
    for (const id of plantIds) {
      if (!id || seen.has(id)) continue;
      seen.add(id);
      ids.push(id);
    }
    // nothing eligible: no choice, and no write that could clobber a real one
    if (ids.length === 0) return null;

    const previous = this.read(workspaceId);
    const at = previous === null ? -1 : ids.indexOf(previous);
    // an unknown or gone previous id starts at the first row
    const next = ids[(at + 1) % ids.length];
    this.write(workspaceId, next);
    return next;
  }

  private key(workspaceId: string): string {
    return `canopy.hero.${workspaceId}`;
  }

  private read(workspaceId: string): string | null {
    try {
      return sessionStorage.getItem(this.key(workspaceId)) ?? this.memory.get(workspaceId) ?? null;
    } catch {
      return this.memory.get(workspaceId) ?? null;
    }
  }

  private write(workspaceId: string, id: string): void {
    this.memory.set(workspaceId, id);
    try {
      sessionStorage.setItem(this.key(workspaceId), id);
    } catch {
      // storage refused: the in-memory map already carries the sequence
    }
  }
}
