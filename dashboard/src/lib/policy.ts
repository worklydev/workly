/** Choices for retry_on_4xx, shared by the queue and schedule forms. */
export const RETRY_4XX = [
  { value: 'some', label: 'Only 408 and 429' },
  { value: 'all', label: 'All 4xx' },
];
