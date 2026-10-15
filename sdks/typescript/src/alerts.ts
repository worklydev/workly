import type { RequestOptions, Workly } from './client.ts';
import { seg } from './util.ts';
import type { AlertChannel, AlertChannelList, AlertChannelType } from './types.ts';

/**
 * Where alerts about failed tasks and schedule runs go: email or Slack.
 *
 * When tasks in a queue or runs of a schedule fail, every channel gets an
 * alert within about a minute, then at most one every 30 minutes per queue
 * or schedule.
 */
export class Alerts {
  #client: Workly;

  constructor(client: Workly) {
    this.#client = client;
  }

  /** The workspace's channels, oldest first, and whether the server can send email. */
  async list(options: RequestOptions = {}): Promise<AlertChannelList> {
    return this.#client.send({ method: 'GET', path: '/v1/alert-channels', idempotent: true, signal: options.signal });
  }

  /** Adds a channel: an email address, or a Slack incoming webhook URL. */
  async create(type: AlertChannelType, target: string, options: RequestOptions = {}): Promise<AlertChannel> {
    // Not idempotent: a resend after it was added gets alert_channel_exists.
    return this.#client.send({
      method: 'POST',
      path: '/v1/alert-channels',
      body: { type, target },
      signal: options.signal,
    });
  }

  async delete(id: string, options: RequestOptions = {}): Promise<void> {
    await this.#client.send({
      method: 'DELETE',
      path: `/v1/alert-channels/${seg(id)}`,
      idempotent: true,
      signal: options.signal,
    });
  }

  /** Sends a test alert to the channel now. Throws an APIError (`alert_failed`) if it can't. */
  async test(id: string, options: RequestOptions = {}): Promise<void> {
    // Not idempotent: a resend sends a second test message.
    await this.#client.send({ method: 'POST', path: `/v1/alert-channels/${seg(id)}/test`, signal: options.signal });
  }
}
