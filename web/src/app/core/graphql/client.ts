import { Injectable, inject } from '@angular/core';
import type { TypedDocumentNode } from '@graphql-typed-document-node/core';
import { print } from 'graphql';
import { SessionService } from '../auth/session';

/** One entry of the GraphQL `errors` array (transport/query level). */
export interface GraphQLErrorItem {
  message: string;
  path?: Array<string | number>;
  extensions?: { code?: string };
}

/**
 * The four outcomes a caller branches on. Payload-level problems (CONFLICT,
 * NOT_FOUND, VALIDATION inside a mutation payload) arrive inside `data` on
 * kind 'ok' — they are results, not failures. Only transport/session
 * failures get their own kind, which is how auth errors stay distinguishable
 * from payload errors.
 */
export type GqlResult<T> =
  | { kind: 'ok'; data: T | null; errors: GraphQLErrorItem[] }
  | { kind: 'auth' }
  | { kind: 'network' }
  | { kind: 'aborted' };

/**
 * Typed GraphQL transport for generated documents (T08). Sends the CSRF
 * token held in memory by SessionService, honours AbortSignal for
 * navigation-away cancellation, and never touches browser storage.
 */
@Injectable({ providedIn: 'root' })
export class GraphQLClient {
  private readonly session = inject(SessionService);

  query<T, V>(doc: TypedDocumentNode<T, V>, variables?: V, signal?: AbortSignal): Promise<GqlResult<T>> {
    return this.exec(doc, variables, signal);
  }

  mutate<T, V>(doc: TypedDocumentNode<T, V>, variables?: V, signal?: AbortSignal): Promise<GqlResult<T>> {
    return this.exec(doc, variables, signal);
  }

  private async exec<T, V>(
    doc: TypedDocumentNode<T, V>,
    variables?: V,
    signal?: AbortSignal,
  ): Promise<GqlResult<T>> {
    const csrf = this.session.info()?.csrfToken;
    let res: Response;
    try {
      res = await fetch('/api/graphql', {
        method: 'POST',
        credentials: 'same-origin',
        signal,
        headers: {
          'Content-Type': 'application/json',
          ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
        },
        body: JSON.stringify({
          query: print(doc),
          ...(variables === undefined ? {} : { variables }),
        }),
      });
    } catch (err) {
      if ((err as { name?: string })?.name === 'AbortError') return { kind: 'aborted' };
      return { kind: 'network' };
    }
    if (res.status === 401) {
      // The server says the session is gone or expired; make the client agree.
      this.session.markExpired();
      return { kind: 'auth' };
    }
    if (res.status === 403) {
      // CSRF/origin mismatch: the session may still be fine, but this call
      // cannot ride it. Do not mark the session expired.
      return { kind: 'auth' };
    }
    let envelope: { data?: T | null; errors?: GraphQLErrorItem[] };
    try {
      envelope = (await res.json()) as { data?: T | null; errors?: GraphQLErrorItem[] };
    } catch {
      return { kind: 'network' };
    }
    // 200/400/413/415 all carry GraphQL envelopes on this API; callers
    // branch on errors/data themselves.
    return { kind: 'ok', data: envelope.data ?? null, errors: envelope.errors ?? [] };
  }
}
