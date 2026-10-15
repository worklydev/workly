import type { ComponentType } from 'react';
import { ApiKeyPrompt } from './components/ApiKeyPrompt';
import { storedKey } from './lib/apiKey';

/**
 * How the dashboard authenticates API requests. The open-source build asks
 * for WORKLY_API_KEY when the server requires one; a hosted build can use
 * cookie sessions and redirect to its own login instead.
 */
export type Auth = {
  /** Bearer token for API requests, if any. */
  token?: () => string | undefined | Promise<string | undefined>;
  /** Fetch credentials mode, e.g. 'include' for cookie sessions. */
  credentials?: RequestCredentials;
  /** Shown instead of the dashboard when the API answers 401. Call `retry` once credentials change. */
  Unauthorized: ComponentType<{ retry: () => void }>;
};

/** The default: an API key, asked for once and kept in this browser. */
export const apiKeyAuth: Auth = {
  token: storedKey,
  Unauthorized: ApiKeyPrompt,
};
