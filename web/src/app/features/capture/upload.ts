import { Injectable, inject } from '@angular/core';
import {
  FinalizeUploadDocument,
  PendingUploadsDocument,
  ReserveUploadDocument,
  type AssetFieldsFragment,
  type AssetPurpose,
} from '@canopy/contracts';
import { SessionService } from '../../core/auth/session';
import { GraphQLClient, type GqlResult } from '../../core/graphql/client';

/** Payload-level problems shared by every media mutation. */
export interface MediaProblem {
  code?: string | null;
  field?: string | null;
  message: string;
}

export type Outcome =
  { ok: true; asset: AssetFieldsFragment } | { ok: false; code: string; message: string };

/** Result of the byte-side PUT against /api/uploads/{id}. */
export type PutOutcome =
  { ok: true; sha256: string } | { ok: false; code: string; message: string };

/** What the poll loop learned about an asset's server-side fate. */
export type Terminal =
  | { outcome: 'ready' }
  | { outcome: 'failed'; errorCode: string }
  | { outcome: 'gone' } // left the pending set without a failure — expired
  | { outcome: 'timeout' };

function parseProblem(status: number, body: string): { code: string; message: string } {
  try {
    const env = JSON.parse(body) as { problems?: { code?: string; message?: string }[] };
    const p = env.problems?.[0];
    if (p?.message) return { code: p.code ?? `HTTP_${status}`, message: p.message };
  } catch {
    // non-JSON body: fall through to the status-derived code
  }
  return { code: `HTTP_${status}`, message: `Upload endpoint returned ${status}.` };
}

/**
 * Byte-side PUT with real progress events (fetch cannot report them).
 * Session cookie rides along; the CSRF header keeps the endpoint honest.
 */
export function putBytes(
  assetId: string,
  body: Blob,
  csrf: string | null,
  onProgress: (pct: number) => void,
): Promise<PutOutcome> {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', `/api/uploads/${assetId}`);
    xhr.withCredentials = true;
    if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf);
    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
    });
    xhr.addEventListener('load', () => {
      if (xhr.status === 200) {
        try {
          const receipt: unknown = JSON.parse(xhr.responseText);
          const sha =
            receipt && typeof receipt === 'object' && 'sha256' in receipt ? receipt.sha256 : null;
          if (typeof sha !== 'string' || !/^[0-9a-f]{64}$/.test(sha)) {
            resolve({
              ok: false,
              code: 'BAD_RESPONSE',
              message: 'The server returned an invalid upload checksum. Retry the upload.',
            });
            return;
          }
          // The server hashes the received bytes. Use its verified receipt,
          // including on LAN HTTP where SubtleCrypto is unavailable.
          resolve({ ok: true, sha256: sha });
          return;
        } catch {
          resolve({ ok: false, code: 'BAD_RESPONSE', message: 'Upload receipt was unreadable.' });
          return;
        }
      }
      resolve({ ok: false, ...parseProblem(xhr.status, xhr.responseText) });
    });
    xhr.addEventListener('error', () =>
      resolve({
        ok: false,
        code: 'NETWORK',
        message:
          'Connection lost mid-upload. The reservation stays open; retry restarts from byte zero.',
      }),
    );
    xhr.send(body);
  });
}

type Mutate = typeof GraphQLClient.prototype.mutate;

/** Injectable so component specs can replace the whole transport seam. */
@Injectable({ providedIn: 'root' })
export class Uploader {
  private readonly gql = inject(GraphQLClient);
  private readonly session = inject(SessionService);

  /** Test seam: specs replace the GraphQL transport; production uses the app client. */
  mutate: Mutate = (doc, vars, signal) => this.gql.mutate(doc, vars, signal);

  /**
   * `clientMutationId` is optional and additive: callers that need a retry to
   * replay the *same* request (rounds actions) pass a cached key, everyone
   * else leaves it out and the variables are byte-identical to before.
   */
  async reserve(
    purpose: AssetPurpose,
    filename: string,
    claimedBytes: number,
    clientMutationId?: string,
  ): Promise<Outcome> {
    const res = await this.mutate(ReserveUploadDocument, {
      input: { purpose, filename, claimedBytes },
      ...(clientMutationId ? { clientMutationId } : {}),
    });
    return this.assetOutcome(
      res,
      (d) =>
        (
          d as {
            reserveUpload?: {
              asset?: AssetFieldsFragment | null;
              problems?: MediaProblem[];
            } | null;
          }
        ).reserveUpload,
    );
  }

  async finalize(assetId: string, sha256: string, clientMutationId?: string): Promise<Outcome> {
    const res = await this.mutate(FinalizeUploadDocument, {
      input: { assetId, sha256 },
      ...(clientMutationId ? { clientMutationId } : {}),
    });
    return this.assetOutcome(
      res,
      (d) =>
        (
          d as {
            finalizeUpload?: {
              asset?: AssetFieldsFragment | null;
              problems?: MediaProblem[];
            } | null;
          }
        ).finalizeUpload,
    );
  }

  /** Owner-scoped pending set (RESERVED/UPLOADED/PROCESSING/FAILED) — the reload-recovery source. */
  async pending(): Promise<AssetFieldsFragment[]> {
    const res = await this.gql.query(PendingUploadsDocument, { first: 100 });
    if (res.kind !== 'ok') return [];
    return (
      (res.data as { pendingUploads?: { items: AssetFieldsFragment[] } } | null)?.pendingUploads
        ?.items ?? []
    );
  }

  /** PUT bytes for a reservation; progress in 0..100. */
  put(assetId: string, body: Blob, onProgress: (pct: number) => void): Promise<PutOutcome> {
    return putBytes(assetId, body, this.session.info()?.csrfToken ?? null, onProgress);
  }

  /**
   * Poll the pending set until the asset leaves it (READY or expired) or fails.
   * The processor is asynchronous by design; the UI says "Processing" meanwhile.
   */
  async waitTerminal(assetId: string, timeoutMs = 120_000, intervalMs = 1_500): Promise<Terminal> {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const items = await this.pending();
      const mine = items.find((a) => a.id === assetId);
      if (!mine) return { outcome: 'gone' }; // attach will tell us which it was
      if (mine.state === 'FAILED')
        return { outcome: 'failed', errorCode: mine.errorCode ?? 'PROCESSING_FAILED' };
      if (Date.now() >= deadline) return { outcome: 'timeout' };
      await new Promise((r) => setTimeout(r, intervalMs));
    }
  }

  private assetOutcome(
    res: GqlResult<object>,
    pick: (
      d: object,
    ) => { asset?: AssetFieldsFragment | null; problems?: MediaProblem[] } | null | undefined,
  ): Outcome {
    if (res.kind === 'auth')
      return {
        ok: false,
        code: 'SESSION',
        message: 'Session expired — sign in again to continue the upload.',
      };
    if (res.kind !== 'ok')
      return {
        ok: false,
        code: 'NETWORK',
        message: 'Could not reach the server. Nothing was uploaded.',
      };
    const payload = res.data ? pick(res.data) : null;
    const problems = payload?.problems ?? [];
    if (problems.length > 0)
      return { ok: false, code: problems[0].code ?? 'VALIDATION', message: problems[0].message };
    if (!payload?.asset)
      return {
        ok: false,
        code: 'EMPTY',
        message: 'The server returned no asset. Nothing was uploaded.',
      };
    return { ok: true, asset: payload.asset };
  }
}
