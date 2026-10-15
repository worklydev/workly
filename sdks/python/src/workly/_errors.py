from __future__ import annotations


class WorklyError(Exception):
    """Base class for every error the SDK raises."""


class APIConnectionError(WorklyError):
    """The server couldn't be reached, or the connection failed mid-request."""


class APIError(WorklyError):
    """The server answered with an error.

    `code` is stable and machine-readable (e.g. `queue_not_found`);
    `message` is for humans.
    """

    def __init__(self, status: int, code: str, message: str) -> None:
        super().__init__(f"{code}: {message} (HTTP {status})")
        self.status = status
        self.code = code
        self.message = message


class BadRequestError(APIError):
    """400: the request was invalid."""


class AuthenticationError(APIError):
    """401: the API key is missing or wrong."""


class PermissionDeniedError(APIError):
    """403: not allowed, or a limit was reached, e.g. a hosted workspace's monthly task quota."""


class NotFoundError(APIError):
    """404: the queue or task doesn't exist."""


class ConflictError(APIError):
    """409: the task is in the wrong state, e.g. retrying a running task."""


class PayloadTooLargeError(APIError):
    """413: the task body is over the server's limit."""


class RateLimitError(APIError):
    """429: too many requests; retried automatically (`max_retries`) before this is raised."""


class SignatureVerificationError(WorklyError):
    """A delivery's signature is missing, wrong or too old."""


_BY_STATUS: dict[int, type[APIError]] = {
    400: BadRequestError,
    401: AuthenticationError,
    403: PermissionDeniedError,
    404: NotFoundError,
    409: ConflictError,
    413: PayloadTooLargeError,
    429: RateLimitError,
}


def api_error(status: int, code: str, message: str) -> APIError:
    return _BY_STATUS.get(status, APIError)(status, code, message)
