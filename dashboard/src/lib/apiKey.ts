export const API_KEY_STORAGE = 'workly.apiKey';

export function storedKey(): string | undefined {
  try {
    return localStorage.getItem(API_KEY_STORAGE) ?? undefined;
  } catch {
    return undefined;
  }
}
