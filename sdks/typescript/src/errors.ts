/** Base class for every error the SDK throws. */
export class WorklyError extends Error {
  override name = 'WorklyError';
}

/** The server couldn't be reached, the connection failed mid-request, or the request timed out. */
export class APIConnectionError extends WorklyError {
  override name = 'APIConnectionError';
}

/**
 * The server answered with an error. `code` is stable and machine-readable
 * (e.g. `queue_not_found`); `message` is for humans.
 */
export class APIError extends WorklyError {
  override name = 'APIError';
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }

  override toString(): string {
    return `${this.name}: ${this.code}: ${this.message} (HTTP ${this.status})`;
  }
}

/** 400: the request was invalid. */
export class BadRequestError extends APIError {
  override name = 'BadRequestError';
}

/** 401: the API key is missing or wrong. */
export class AuthenticationError extends APIError {
  override name = 'AuthenticationError';
}

/** 403: not allowed, or a limit was reached, e.g. a hosted workspace's monthly task quota. */
export class PermissionDeniedError extends APIError {
  override name = 'PermissionDeniedError';
}

/** 404: the queue or task doesn't exist. */
export class NotFoundError extends APIError {
  override name = 'NotFoundError';
}

/** 409: the task is in the wrong state, e.g. retrying a running task. */
export class ConflictError extends APIError {
  override name = 'ConflictError';
}

/** 413: the task body is over the server's limit. */
export class PayloadTooLargeError extends APIError {
  override name = 'PayloadTooLargeError';
}

/** 429: too many requests; retried automatically (`maxRetries`) before this is thrown. */
export class RateLimitError extends APIError {
  override name = 'RateLimitError';
}

/** A delivery's signature is missing, wrong or too old. */
export class SignatureVerificationError extends WorklyError {
  override name = 'SignatureVerificationError';
}

const byStatus: Record<number, typeof APIError> = {
  400: BadRequestError,
  401: AuthenticationError,
  403: PermissionDeniedError,
  404: NotFoundError,
  409: ConflictError,
  413: PayloadTooLargeError,
  429: RateLimitError,
};

export function apiError(status: number, code: string, message: string): APIError {
  return new (byStatus[status] ?? APIError)(status, code, message);
}
