import type { RequestOptions, Workly } from './client.ts';
import { seg } from './util.ts';
import type { Group, GroupPage, GroupSpec, GroupStatus } from './types.ts';

export interface GroupsOptions extends RequestOptions {
  status?: GroupStatus | undefined;
  name?: string | undefined;
  /** Groups per request, 1–200 (server default 50). */
  pageSize?: number | undefined;
}

export interface GroupsPageOptions extends RequestOptions {
  status?: GroupStatus | undefined;
  name?: string | undefined;
  /** The previous page's `nextCursor`. */
  cursor?: string | undefined;
  /** 1–200 (server default 50). */
  limit?: number | undefined;
}

/**
 * Task groups: a set of tasks, and a callback task once all have finished.
 *
 * Create a group, enqueue tasks with its `groupId`, then close it. Once it is
 * closed and every task has succeeded, failed or been cancelled, it completes
 * and enqueues its `onComplete` callback, once.
 */
export class Groups {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /**
   * Creates an open group. If `idempotencyKey` matches an existing group, that
   * group is returned and nothing is created.
   */
  async create(spec: GroupSpec = {}, options: RequestOptions = {}): Promise<Group> {
    return this.#client.send({
      method: 'POST',
      path: '/v1/groups',
      body: spec,
      // Without a key, resending could create a second group.
      idempotent: spec.idempotencyKey !== undefined,
      signal: options.signal,
    });
  }

  async get(id: string, options: RequestOptions = {}): Promise<Group> {
    return this.#client.send({
      method: 'GET',
      path: `/v1/groups/${seg(id)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /**
   * Stops the group accepting tasks, so it completes once they have finished.
   * A group whose tasks have all finished completes at once. Closing a closed
   * group does nothing.
   */
  async close(id: string, options: RequestOptions = {}): Promise<Group> {
    return this.#client.send({
      method: 'POST',
      path: `/v1/groups/${seg(id)}/close`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Groups, newest first, fetching pages as needed. */
  async *list(options: GroupsOptions = {}): AsyncGenerator<Group, void, undefined> {
    const { pageSize, ...rest } = options;
    let cursor: string | undefined;
    do {
      const page = await this.listPage({ ...rest, ...(cursor && { cursor }), ...(pageSize && { limit: pageSize }) });
      yield* page.groups;
      cursor = page.nextCursor;
    } while (cursor);
  }

  /** One page of `list`. Pass the previous page's `nextCursor` as `cursor`. */
  async listPage(options: GroupsPageOptions = {}): Promise<GroupPage> {
    return this.#client.send({
      method: 'GET',
      path: '/v1/groups',
      query: { status: options.status, name: options.name, cursor: options.cursor, limit: options.limit?.toString() },
      idempotent: true,
      signal: options.signal,
    });
  }
}
